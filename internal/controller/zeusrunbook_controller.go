
package controller

import (
	"context"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	zeusv1 "github.com/zeus-broadcast/zeus-kernel/api/v1alpha1"
	"github.com/zeus-broadcast/zeus-kernel/internal/fabric"
)

const zeusFinalizer = "zeus.broadcast/finalizer"

type ZeusRunbookReconciler struct {
	client.Client
	Scheme *runtime.Scheme
	Fabric fabric.Client
}

func (r *ZeusRunbookReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	var rb zeusv1.ZeusRunbook
	if err := r.Get(ctx, req.NamespacedName, &rb); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if !rb.DeletionTimestamp.IsZero() {
		if controllerutil.ContainsFinalizer(&rb, zeusFinalizer) {
			for _, p := range rb.Spec.NAPs {
				if err := r.Fabric.DeleteReceiver(ctx, rb.Namespace, p.Name); err != nil {
					logger.Error(err, "delete receiver", "nap", p.Name)
					return ctrl.Result{}, err
				}
				if err := r.deleteDeployment(ctx, rb.Namespace, p.Name); err != nil {
					return ctrl.Result{}, err
				}
			}
			controllerutil.RemoveFinalizer(&rb, zeusFinalizer)
			return ctrl.Result{}, r.Update(ctx, &rb)
		}
		return ctrl.Result{}, nil
	}

	if !controllerutil.ContainsFinalizer(&rb, zeusFinalizer) {
		controllerutil.AddFinalizer(&rb, zeusFinalizer)
		if err := r.Update(ctx, &rb); err != nil {
			return ctrl.Result{}, err
		}
	}

	// Capability guard BEFORE any resource lookup: dual-path merge is Phase 2.
	if rb.Spec.Ingress != nil && rb.Spec.Ingress.Contribution != nil {
		c := rb.Spec.Ingress.Contribution
		if c.Redundancy == "hitless" || len(c.Paths) > 1 {
			return r.setPhase(ctx, &rb, "Degraded", "DualPathUnsupported",
				"dual-path merge is Phase 2; apply examples/mvp-single-path.yaml instead")
		}
	}

	for _, p := range rb.Spec.NAPs {
		var nap zeusv1.ZeusApplicationPackage
		if err := r.Get(ctx, types.NamespacedName{Namespace: rb.Namespace, Name: p.NAP}, &nap); err != nil {
			if apierrors.IsNotFound(err) {
				return r.setPhase(ctx, &rb, "Reconciling", "NAPNotFound", p.NAP)
			}
			return ctrl.Result{}, err
		}
		if nap.Status.Phase != "Validated" {
			return r.setPhase(ctx, &rb, "Reconciling", "NAPNotValidated", p.NAP)
		}
	}

	if c := rb.Spec.Ingress; c != nil && c.Contribution != nil {
		for _, p := range c.Contribution.Paths {
			if p.SRT == nil {
				continue
			}
			var sec corev1.Secret
			err := r.Get(ctx, types.NamespacedName{Namespace: rb.Namespace, Name: p.SRT.PassphraseSecretRef.Name}, &sec)
			if err != nil {
				if apierrors.IsNotFound(err) {
					return r.setPhase(ctx, &rb, "Degraded", "SecretNotFound", p.SRT.PassphraseSecretRef.Name)
				}
				return ctrl.Result{}, err
			}
			if len(sec.Data[p.SRT.PassphraseSecretRef.Key]) < 20 {
				return r.setPhase(ctx, &rb, "Degraded", "PassphraseTooShort",
					"passphrase must be at least 20 characters; regenerate with hack/gen-passphrase.sh")
			}
		}
	}

	for _, p := range rb.Spec.NAPs {
		flowID := rb.Name + "-" + p.Name
		if err := r.Fabric.EnsureReceiver(ctx, rb.Namespace, p.Name, flowID, p.Name, "tcp", &rb); err != nil {
			if apierrors.IsNotFound(err) {
				return r.setPhase(ctx, &rb, "Degraded", "FabricNotInstalled",
					"mxl-k8s CRDs not found; install the mxl chart")
			}
			if apierrors.IsConflict(err) {
				return ctrl.Result{Requeue: true}, nil
			}
			logger.Error(err, "fabric ensure failed", "nap", p.Name)
			return ctrl.Result{}, err
		}

		var nap zeusv1.ZeusApplicationPackage
		if err := r.Get(ctx, types.NamespacedName{Namespace: rb.Namespace, Name: p.NAP}, &nap); err != nil {
			return ctrl.Result{}, err
		}
		if err := r.ensureDeployment(ctx, &rb, p, &nap); err != nil {
			return r.setPhase(ctx, &rb, "Degraded", "DeployError", err.Error())
		}
	}

	return r.setPhase(ctx, &rb, "Live", "WorkflowReady", "")
}

func (r *ZeusRunbookReconciler) ensureDeployment(ctx context.Context, rb *zeusv1.ZeusRunbook, p zeusv1.NAPPlacement, nap *zeusv1.ZeusApplicationPackage) error {
	domainRef := nap.Spec.CRM.MediaIO.DomainRef
	if domainRef == "" {
		return nil
	}
	readOnly := !nap.Spec.CRM.MediaIO.MXLWriter

	dep := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: p.Name, Namespace: rb.Namespace}}
	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, dep, func() error {
		dep.Spec = appsv1.DeploymentSpec{
			Replicas: int32Ptr(1),
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": p.Name}},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": p.Name}},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{
						Name:  "media",
						Image: nap.Spec.Image,
						SecurityContext: &corev1.SecurityContext{
							RunAsNonRoot:             boolPtr(true),
							ReadOnlyRootFilesystem:   boolPtr(true),
							AllowPrivilegeEscalation: boolPtr(false),
							Capabilities: &corev1.Capabilities{
								Drop: []corev1.Capability{"ALL"},
								Add:  []corev1.Capability{"IPC_LOCK"},
							},
						},
						VolumeMounts: []corev1.VolumeMount{{
							Name:      "mxl-domain",
							MountPath: "/run/mxl/domain/" + domainRef,
							ReadOnly:  readOnly,
						}},
					}},
					Volumes: []corev1.Volume{{
						Name: "mxl-domain",
						VolumeSource: corev1.VolumeSource{
							HostPath: &corev1.HostPathVolumeSource{
								Path: "/run/mxl/domain",
								Type: ptrHostPathType(corev1.HostPathDirectoryOrCreate),
							},
						},
					}},
				},
			},
		}
		return controllerutil.SetControllerReference(rb, dep, r.Scheme)
	})
	return err
}

func (r *ZeusRunbookReconciler) deleteDeployment(ctx context.Context, namespace, name string) error {
	dep := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace}}
	err := r.Delete(ctx, dep)
	if apierrors.IsNotFound(err) {
		return nil
	}
	return err
}

func (r *ZeusRunbookReconciler) setPhase(ctx context.Context, rb *zeusv1.ZeusRunbook, phase, reason, msg string) (ctrl.Result, error) {
	if rb.Status.Phase == phase && len(rb.Status.Conditions) > 0 && rb.Status.Conditions[0].Reason == reason {
		return ctrl.Result{}, nil
	}
	rb.Status.Phase = phase
	rb.Status.Conditions = []metav1.Condition{{
		Type:               "Ready",
		Status:             metav1.ConditionTrue,
		Reason:             reason,
		Message:            msg,
		LastTransitionTime: metav1.Now(),
	}}
	log.FromContext(ctx).Info("phase_transition",
		"runbook", rb.Name, "phase", phase, "reason", reason,
		"actor", "controller", "causal_chain_id", string(rb.UID))
	RunbookPhase.WithLabelValues(phase).Set(1)
	if err := r.Status().Update(ctx, rb); err != nil {
		return ctrl.Result{RequeueAfter: 5 * time.Second}, err
	}
	return ctrl.Result{}, nil
}

func (r *ZeusRunbookReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&zeusv1.ZeusRunbook{}).
		Owns(&appsv1.Deployment{}).
		Complete(r)
}

func int32Ptr(i int32) *int32                                  { return &i }
func boolPtr(b bool) *bool                                     { return &b }
func ptrHostPathType(t corev1.HostPathType) *corev1.HostPathType { return &t }
