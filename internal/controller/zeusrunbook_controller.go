package controller

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
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
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	zeusv1 "github.com/zeus-broadcast/zeus-kernel/api/v1alpha1"
	"github.com/zeus-broadcast/zeus-kernel/internal/fabric"
	"github.com/zeus-broadcast/zeus-kernel/internal/transport/srt"
)

const zeusFinalizer = "zeus.broadcast/finalizer"

type ZeusRunbookReconciler struct {
	client.Client
	Scheme        *runtime.Scheme
	Fabric        fabric.Client
	BridgeFactory func() srt.Bridge
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
					ReconcileErrors.WithLabelValues("delete_receiver").Inc()
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
		flowID := zeusFlowID(string(rb.UID), p.Name)

		// The deployment is ensured first so a live pod exists before we
		// point the receiver at it.
		var nap zeusv1.ZeusApplicationPackage
		if err := r.Get(ctx, types.NamespacedName{Namespace: rb.Namespace, Name: p.NAP}, &nap); err != nil {
			return ctrl.Result{}, err
		}
		if err := r.ensureDeployment(ctx, &rb, p, &nap); err != nil {
			return r.setPhase(ctx, &rb, "Degraded", "DeployError", err.Error())
		}

		podName, err := r.findAppPod(ctx, rb.Namespace, p.Name)
		if err != nil {
			return r.setPhase(ctx, &rb, "Degraded", "PodLookupError", err.Error())
		}
		if podName == "" {
			if _, serr := r.setPhase(ctx, &rb, "Reconciling", "PodNotFound",
				fmt.Sprintf("no pod with app=%s yet; deployment may still be pulling", p.Name)); serr != nil {
				return ctrl.Result{}, serr
			}
			return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
		}

		if err := r.Fabric.EnsureReceiver(ctx, rb.Namespace, p.Name, flowID, podName, "tcp", &rb); err != nil {
			if apierrors.IsNotFound(err) {
				return r.setPhase(ctx, &rb, "Degraded", "FabricNotInstalled",
					"mxl-k8s CRDs not found; install the mxl chart")
			}
			if apierrors.IsConflict(err) {
				return ctrl.Result{Requeue: true}, nil
			}
			logger.Error(err, "fabric ensure failed", "nap", p.Name)
			ReconcileErrors.WithLabelValues("fabric_ensure").Inc()
			return ctrl.Result{}, err
		}
	}

	// Phase 2: measure real path health for each SRT contribution path and
	// write it to status.pathHealth[]. A down path degrades the runbook
	// instead of letting it reach Live on a broken WAN leg.
	pathHealth, anyDown := r.computePathHealth(ctx, rb)
	if len(pathHealth) > 0 {
		rb.Status.PathHealth = pathHealth
		if anyDown {
			return r.setPhase(ctx, &rb, "Degraded", "PathDown",
				"one or more SRT contribution paths are down")
		}
	}

	return r.setPhase(ctx, &rb, "Live", "WorkflowReady", "")
}

// computePathHealth measures the health of each SRT contribution path using
// the bridge. A new bridge is started per path (with the path's SRT spec and
// passphrase resolved from the referenced Secret), Health is measured, then
// the bridge is stopped. Non-SRT paths and runbooks without a bridge factory
// are skipped.
func (r *ZeusRunbookReconciler) computePathHealth(ctx context.Context, rb zeusv1.ZeusRunbook) ([]zeusv1.PathHealth, bool) {
	if rb.Spec.Ingress == nil || rb.Spec.Ingress.Contribution == nil || r.BridgeFactory == nil {
		return nil, false
	}
	var paths []zeusv1.PathHealth
	anyDown := false
	for _, p := range rb.Spec.Ingress.Contribution.Paths {
		if p.SRT == nil {
			continue
		}
		name := p.Name
		if name == "" {
			name = p.SRT.RemoteHost
		}
		ph := zeusv1.PathHealth{Name: name, Transport: "srt", State: "down"}

		// Resolve the passphrase from the referenced Secret.
		var sec corev1.Secret
		if err := r.Get(ctx, types.NamespacedName{Namespace: rb.Namespace, Name: p.SRT.PassphraseSecretRef.Name}, &sec); err != nil {
			paths = append(paths, ph)
			anyDown = true
			continue
		}
		passphrase, ok := sec.Data[p.SRT.PassphraseSecretRef.Key]
		if !ok {
			paths = append(paths, ph)
			anyDown = true
			continue
		}

		// Start a fresh bridge for this path, measure health, then stop it.
		bridge := r.BridgeFactory()
		if err := bridge.Start(ctx, *p.SRT, passphrase); err != nil {
			paths = append(paths, ph)
			anyDown = true
			continue
		}
		defer bridge.Stop(ctx)

		h, err := bridge.Health(ctx)
		if err != nil {
			paths = append(paths, ph)
			anyDown = true
			continue
		}
		ph = h
		ph.Name = name
		if ph.State == "" {
			ph.State = "down"
		}
		if ph.State == "down" {
			anyDown = true
		}
		paths = append(paths, ph)
	}
	return paths, anyDown
}

// findAppPod returns the name of the oldest non-terminating pod carrying
// the app=<name> label in the namespace, or "" if none exists yet.
func (r *ZeusRunbookReconciler) findAppPod(ctx context.Context, namespace, app string) (string, error) {
	var list corev1.PodList
	if err := r.List(ctx, &list, client.InNamespace(namespace), client.MatchingLabels{"app": app}); err != nil {
		return "", err
	}
	for i := range list.Items {
		pod := &list.Items[i]
		if pod.DeletionTimestamp != nil {
			continue
		}
		return pod.Name, nil
	}
	return "", nil
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
	prevPhase := rb.Status.Phase
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
	// Keep the gauge accurate across transitions: only the current phase is 1.
	if prevPhase != phase {
		RunbookPhase.WithLabelValues(phase).Set(1)
		if prevPhase != "" {
			RunbookPhase.WithLabelValues(prevPhase).Set(0)
		}
	}
	if err := r.Status().Update(ctx, rb); err != nil {
		ReconcileErrors.WithLabelValues("status_update").Inc()
		return ctrl.Result{RequeueAfter: 5 * time.Second}, err
	}
	return ctrl.Result{}, nil
}

func (r *ZeusRunbookReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&zeusv1.ZeusRunbook{}).
		Owns(&appsv1.Deployment{}).
		// A runbook that is waiting on a NAP must be re-reconciled when that
		// NAP is validated (or its image/signature changes). Without this
		// watch it would stay in Reconciling until the runbook itself changed.
		Watches(&zeusv1.ZeusApplicationPackage{}, handler.EnqueueRequestsFromMapFunc(r.runbooksForNAP)).
		Complete(r)
}

// runbooksForNAP maps a NAP event to every runbook in the same namespace that
// references it by name.
func (r *ZeusRunbookReconciler) runbooksForNAP(ctx context.Context, obj client.Object) []reconcile.Request {
	nap, ok := obj.(*zeusv1.ZeusApplicationPackage)
	if !ok {
		return nil
	}
	var runbooks zeusv1.ZeusRunbookList
	if err := r.List(ctx, &runbooks, client.InNamespace(nap.Namespace)); err != nil {
		log.FromContext(ctx).Error(err, "list runbooks for NAP", "nap", nap.Name)
		return nil
	}
	var requests []reconcile.Request
	for i := range runbooks.Items {
		rb := &runbooks.Items[i]
		for _, placement := range rb.Spec.NAPs {
			if placement.NAP == nap.Name {
				requests = append(requests, reconcile.Request{NamespacedName: types.NamespacedName{
					Namespace: rb.Namespace, Name: rb.Name,
				}})
				break
			}
		}
	}
	return requests
}

func int32Ptr(i int32) *int32                                    { return &i }
func boolPtr(b bool) *bool                                       { return &b }
func ptrHostPathType(t corev1.HostPathType) *corev1.HostPathType { return &t }

// zeusFlowID derives a deterministic RFC 4122 version-5-style UUID from the
// runbook UID and the NAP placement name. The mxl-k8s MxlReceiver CRD
// validates spec.flowID against a UUID pattern, so the previous
// "<runbook>-<nap>" value was rejected at admission. Deterministic output
// keeps re-reconciliation idempotent.
func zeusFlowID(uid, nap string) string {
	h := sha1.Sum([]byte(uid + "/" + nap))
	u := h[:16]
	u[6] = (u[6] & 0x0f) | 0x50
	u[8] = (u[8] & 0x3f) | 0x80
	s := hex.EncodeToString(u)
	return s[0:8] + "-" + s[8:12] + "-" + s[12:16] + "-" + s[16:20] + "-" + s[20:32]
}
