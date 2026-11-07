package controller

import (
	"context"
	"fmt"
	"time"

	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	zeusv1 "github.com/zeus-broadcast/zeus-kernel/api/v1alpha1"
	"github.com/zeus-broadcast/zeus-kernel/internal/control"
	"github.com/zeus-broadcast/zeus-kernel/internal/nmos"
)

// RoutingClient is the subset of the control adapter registry used by the
// NAP reconciler. Defined as an interface so tests can inject a fake.
type RoutingClient interface {
	GetAdapter(protocol string) (control.Adapter, error)
}

// ZeusApplicationPackageReconciler reconciles ZeusApplicationPackage resources.
type ZeusApplicationPackageReconciler struct {
	client.Client
	Scheme *runtime.Scheme
	NMOS   NMOSClient
	Router RoutingClient
}

// NMOSClient is the subset of the NMOS client used by the NAP reconciler.
// Defined as an interface so tests can inject a fake.
type NMOSClient interface {
	FindSender(ctx context.Context, match nmos.SenderMatch) (*nmos.Resource, error)
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
		NAPValidationTotal.WithLabelValues("rejected").Inc()
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

	// Phase 2 Week 9: IS-04 sender discovery. When senderMatch is enabled,
	// query the NMOS registry for a sender matching the NAP's media criteria.
	// If no matching sender is found, the NAP cannot be validated.
	if nap.Spec.NMOS.IS04 && nap.Spec.NMOS.SenderMatch {
		if r.NMOS == nil {
			return reject("NMOSClientNotConfigured")
		}
		start := time.Now()
		sender, err := r.NMOS.FindSender(ctx, nmos.SenderMatch{
			MediaType: nap.Spec.NMOS.MediaType,
			Essence:   nap.Spec.NMOS.Essence,
			Sampling:  nap.Spec.NMOS.Sampling,
		})
		NmosDiscoveryDuration.WithLabelValues("FindSender").Observe(time.Since(start).Seconds())
		if err != nil {
			return reject("NMOSNoSenderFound: " + err.Error())
		}
		if sender == nil {
			return reject("NMOSNoSenderFound: no matching sender in registry")
		}
		logger.Info("nmos_sender_found", "nap", nap.Name, "sender", sender.ID, "senderLabel", sender.Label)
	}

	// Phase 3: Routing integration. When a NAP has a routing provider
	// configured, establish the route before marking Validated.
	if nap.Spec.Routing.Provider != "" {
		if r.Router == nil {
			return reject("RouterClientNotConfigured")
		}
		adapter, err := r.Router.GetAdapter(nap.Spec.Routing.Provider)
		if err != nil {
			return reject("RouterAdapterNotFound: " + err.Error())
		}

		if err := adapter.Connect(ctx); err != nil {
			return reject("RouterConnectFailed: " + err.Error())
		}
		defer adapter.Disconnect(ctx)

		sourceID := nap.Spec.Routing.SourceID
		destID := nap.Spec.Routing.DestinationID
		if sourceID == "" || destID == "" {
			return reject("RouterSourceDestinationMissing")
		}

		if err := adapter.Route(ctx, sourceID, destID); err != nil {
			return reject("RouterRouteFailed: " + err.Error())
		}
		logger.Info("router_route_established", "nap", nap.Name, "source", sourceID, "destination", destID)
	}

	nap.Status.Phase = "Validated"
	nap.Status.ObservedGen = nap.Generation
	NAPValidationTotal.WithLabelValues("validated").Inc()
	logger.Info("nap_validated", "nap", nap.Name)
	return ctrl.Result{}, r.Status().Update(ctx, &nap)
}

func (r *ZeusApplicationPackageReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).For(&zeusv1.ZeusApplicationPackage{}).Complete(r)
}

// ensureRoute ensures a route is established for a NAP with routing configured.
func (r *ZeusApplicationPackageReconciler) ensureRoute(ctx context.Context, nap *zeusv1.ZeusApplicationPackage) error {
	if r.Router == nil {
		return fmt.Errorf("router client not configured")
	}
	if nap.Spec.Routing.Provider == "" {
		return nil // no routing configured
	}

	adapter, err := r.Router.GetAdapter(nap.Spec.Routing.Provider)
	if err != nil {
		return fmt.Errorf("router adapter not found: %w", err)
	}

	if err := adapter.Connect(ctx); err != nil {
		return fmt.Errorf("router connect failed: %w", err)
	}
	defer adapter.Disconnect(ctx)

	if nap.Spec.Routing.SourceID == "" || nap.Spec.Routing.DestinationID == "" {
		return fmt.Errorf("router source/destination ID missing")
	}

	if err := adapter.Route(ctx, nap.Spec.Routing.SourceID, nap.Spec.Routing.DestinationID); err != nil {
		return fmt.Errorf("router route failed: %w", err)
	}

	return nil
}
