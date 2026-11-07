package controller

import (
	"context"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	zeusv1 "github.com/zeus-broadcast/zeus-kernel/api/v1alpha1"
)

// NAPRegistryReconciler reconciles NAPRegistry resources.
type NAPRegistryReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// Reconcile handles NAPRegistry events.
func (r *NAPRegistryReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	var reg zeusv1.NAPRegistry
	if err := r.Get(ctx, req.NamespacedName, &reg); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// Set phase to Pending on first creation if not already set.
	if reg.Status.Phase == "" {
		reg.Status.Phase = zeusv1.NAPRegistryPending
		reg.Status.ObservedGeneration = reg.Generation
		if err := r.Status().Update(ctx, &reg); err != nil {
			return ctrl.Result{}, err
		}
		logger.Info("nap registry entry created", "nap", reg.Spec.NAPName, "phase", reg.Status.Phase)
		return ctrl.Result{}, nil
	}

	// Re-validate when the spec changes.
	if reg.Status.ObservedGeneration != reg.Generation {
		reg.Status.ObservedGeneration = reg.Generation
		if err := r.Status().Update(ctx, &reg); err != nil {
			return ctrl.Result{}, err
		}
		logger.Info("nap registry entry updated", "nap", reg.Spec.NAPName, "generation", reg.Generation)
	}

	return ctrl.Result{}, nil
}

// SetupWithManager registers the NAPRegistry controller.
func (r *NAPRegistryReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&zeusv1.NAPRegistry{}).
		Complete(r)
}

// ApproveNAPRegistry approves a NAP registry entry.
func (r *NAPRegistryReconciler) ApproveNAPRegistry(ctx context.Context, name, namespace string) error {
	var reg zeusv1.NAPRegistry
	if err := r.Get(ctx, client.ObjectKey{Namespace: namespace, Name: name}, &reg); err != nil {
		return err
	}

	reg.Status.Phase = zeusv1.NAPRegistryApproved
	now := metav1.Now()
	reg.Status.ApprovedAt = &now
	reg.Status.RejectedAt = nil
	reg.Status.RejectionReason = ""
	reg.Status.ObservedGeneration = reg.Generation

	return r.Status().Update(ctx, &reg)
}

// RejectNAPRegistry rejects a NAP registry entry.
func (r *NAPRegistryReconciler) RejectNAPRegistry(ctx context.Context, name, namespace, reason string) error {
	var reg zeusv1.NAPRegistry
	if err := r.Get(ctx, client.ObjectKey{Namespace: namespace, Name: name}, &reg); err != nil {
		return err
	}

	reg.Status.Phase = zeusv1.NAPRegistryRejected
	now := metav1.Now()
	reg.Status.RejectedAt = &now
	reg.Status.ApprovedAt = nil
	reg.Status.RejectionReason = reason
	reg.Status.ObservedGeneration = reg.Generation

	return r.Status().Update(ctx, &reg)
}
