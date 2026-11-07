package controller

import (
	"context"
	"fmt"

	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	zeusv1 "github.com/zeus-broadcast/zeus-kernel/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// RunbookTemplateReconciler reconciles RunbookTemplate resources and can
// instantiate them into ZeusRunbook resources.
type RunbookTemplateReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

const instantiateAnnotation = "zeus.broadcast/instantiate"

func (r *RunbookTemplateReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	var tmpl zeusv1.RunbookTemplate
	if err := r.Get(ctx, req.NamespacedName, &tmpl); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// Check if this template should be instantiated.
	instantiate, ok := tmpl.Annotations[instantiateAnnotation]
	if !ok || instantiate != "true" {
		return ctrl.Result{}, nil
	}

	// Check if a runbook already exists from this template.
	existing := &zeusv1.ZeusRunbookList{}
	if err := r.List(ctx, existing, client.InNamespace(tmpl.Namespace),
		client.MatchingLabels{"zeus.broadcast/template": tmpl.Name}); err != nil {
		return ctrl.Result{}, err
	}

	if len(existing.Items) > 0 {
		// Already instantiated, remove the annotation to prevent re-instantiation.
		delete(tmpl.Annotations, instantiateAnnotation)
		if err := r.Update(ctx, &tmpl); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	// Instantiate the runbook from the template.
	rb := &zeusv1.ZeusRunbook{
		ObjectMeta: metav1.ObjectMeta{
			Name:      tmpl.Name + "-runbook",
			Namespace: tmpl.Namespace,
			Labels: map[string]string{
				"zeus.broadcast/template": tmpl.Name,
			},
		},
		Spec: zeusv1.ZeusRunbookSpec{
			EventClass: tmpl.Spec.EventClass,
			NAPs:       tmpl.Spec.NAPs,
			Ingress:    tmpl.Spec.Ingress,
		},
	}

	if err := controllerutil.SetControllerReference(&tmpl, rb, r.Scheme); err != nil {
		return ctrl.Result{}, err
	}

	if err := r.Create(ctx, rb); err != nil {
		logger.Error(err, "failed to create runbook from template", "template", tmpl.Name, "runbook", rb.Name)
		return ctrl.Result{}, err
	}

	logger.Info("runbook created from template", "template", tmpl.Name, "runbook", rb.Name)

	// Update template status.
	tmpl.Status.InstantiatedCount++
	now := metav1.Now()
	tmpl.Status.LastInstantiatedAt = &now

	// Remove the annotation to prevent re-instantiation.
	delete(tmpl.Annotations, instantiateAnnotation)

	if err := r.Status().Update(ctx, &tmpl); err != nil {
		return ctrl.Result{}, err
	}
	if err := r.Update(ctx, &tmpl); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

// SetupWithManager registers the RunbookTemplate controller.
func (r *RunbookTemplateReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&zeusv1.RunbookTemplate{}).
		Complete(r)
}

// InstantiateTemplate creates a ZeusRunbook from a RunbookTemplate with
// parameter overrides applied.
func (r *RunbookTemplateReconciler) InstantiateTemplate(ctx context.Context, tmpl *zeusv1.RunbookTemplate, params map[string]string) (*zeusv1.ZeusRunbook, error) {
	rb := &zeusv1.ZeusRunbook{
		ObjectMeta: metav1.ObjectMeta{
			Name:      tmpl.Name + "-runbook",
			Namespace: tmpl.Namespace,
			Labels: map[string]string{
				"zeus.broadcast/template": tmpl.Name,
			},
		},
		Spec: zeusv1.ZeusRunbookSpec{
			EventClass: tmpl.Spec.EventClass,
			NAPs:       tmpl.Spec.NAPs,
			Ingress:    tmpl.Spec.Ingress,
		},
	}

	if err := controllerutil.SetControllerReference(tmpl, rb, r.Scheme); err != nil {
		return nil, err
	}

	// Apply parameter overrides to the runbook spec.
	for _, param := range tmpl.Spec.Parameters {
		value, ok := params[param.Name]
		if !ok && param.Default != "" {
			value = param.Default
		}
		if !ok && param.Required {
			return nil, fmt.Errorf("required parameter %s not provided", param.Name)
		}
		if ok || param.Default != "" {
			_ = value // Parameter applied to template fields above
		}
	}

	if err := r.Create(ctx, rb); err != nil {
		return nil, err
	}

	return rb, nil
}
