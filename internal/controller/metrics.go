package controller

import (
	"github.com/prometheus/client_golang/prometheus"
	crmetrics "sigs.k8s.io/controller-runtime/pkg/metrics"
)

var (
	ReconcileErrors = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "zeus_reconcile_errors_total",
		Help: "Reconcile errors by reason",
	}, []string{"reason"})
	RunbookPhase = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "zeus_runbook_phase",
		Help: "Runbooks currently in each phase",
	}, []string{"phase"})
	// PathHealthState reports the health state of each SRT/RIST path.
	// state: "up", "degraded", or "down".
	PathHealthState = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "zeus_path_health_state",
		Help: "Path health state per WAN contribution path (1=healthy, 0=unhealthy)",
	}, []string{"path", "transport", "state"})
	// NAPValidationTotal counts NAP validation outcomes.
	NAPValidationTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "zeus_nap_validation_total",
		Help: "Total NAP validation attempts by outcome",
	}, []string{"outcome"})
	// MxlReceiverBound reports whether an MxlReceiver is bound to a live pod.
	MxlReceiverBound = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "zeus_mxl_receiver_bound",
		Help: "Whether an MxlReceiver is bound to a live pod (1=bound, 0=not bound)",
	}, []string{"receiver", "pod"})
	// NmosDiscoveryDuration tracks IS-04 discovery latency.
	NmosDiscoveryDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "zeus_nmos_discovery_duration_seconds",
		Help:    "Duration of NMOS IS-04 discovery operations",
		Buckets: []float64{0.01, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10},
	}, []string{"operation"})
)

func init() {
	// controller-runtime v0.25 serves /metrics from its own Registry, not
	// prometheus.DefaultRegisterer. Registering there would make the zeus_*
	// metrics invisible to the metrics endpoint.
	crmetrics.Registry.MustRegister(
		ReconcileErrors,
		RunbookPhase,
		PathHealthState,
		NAPValidationTotal,
		MxlReceiverBound,
		NmosDiscoveryDuration,
	)
	// Publish zero-valued children so metric families are always exported,
	// even before the first event occurs.
	ReconcileErrors.WithLabelValues("startup")
	RunbookPhase.WithLabelValues("Live")
	RunbookPhase.WithLabelValues("Reconciling")
	RunbookPhase.WithLabelValues("Degraded")
	RunbookPhase.WithLabelValues("Rejected")
	PathHealthState.WithLabelValues("unknown", "unknown", "down")
	NAPValidationTotal.WithLabelValues("validated")
	NAPValidationTotal.WithLabelValues("rejected")
	MxlReceiverBound.WithLabelValues("unknown", "unknown")
}
