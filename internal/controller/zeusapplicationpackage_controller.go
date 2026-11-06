package controller

import (
	"context"

	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	zeusv1 "github.com/zeus-broadcast/zeus-kernel/api/v1alpha1"
)

type ZeusApplicationPackageReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

func (r *ZeusApplicationPackageReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	var nap zeusv1.ZeusApplicationPackage
	if err := r.Get(ctx, req.NamespacedName, &nap); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	// Re-validate when the spec changes. Without the generation check a NAP
	// that was edited (new image or signature) keeps its old verdict forever.
	if nap.Status.Phase != "" && nap.Status.ObservedGen == nap.Generation {
		return ctrl.Result{}, nil
	}

	reject := func(reason string) (ctrl.Result, error) {
		nap.Status.Phase = "Rejected"
		nap.Status.ObservedGen = nap.Generation
		logger.Info("nap_rejected", "nap", nap.Name, "reason", reason)
		return ctrl.Result{}, r.Status().Update(ctx, &nap)
	}

	if nap.Spec.Image == "" || nap.Spec.ImageSignature == "" {
		return reject("ImageOrSignatureMissing")
	}
	if nap.Spec.CRM.CPU == "" || nap.Spec.CRM.Memory == "" {
		return reject("CRMIncomplete")
	}
	io := nap.Spec.CRM.MediaIO
	if (io.MXLReader || io.MXLWriter) && io.DomainRef == "" {
		return reject("MXLWithoutDomainRef")
	}
	if (io.MXLReader || io.MXLWriter) && nap.Spec.NMOS.Transport != "urn:x-nmos:transport:mxl" {
		return reject("MXLWithoutMXLTransport")
	}

	nap.Status.Phase = "Validated"
	nap.Status.ObservedGen = nap.Generation
	logger.Info("nap_validated", "nap", nap.Name)
	return ctrl.Result{}, r.Status().Update(ctx, &nap)
}

func (r *ZeusApplicationPackageReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).For(&zeusv1.ZeusApplicationPackage{}).Complete(r)
}
