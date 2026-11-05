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
)

func init() {
	// controller-runtime v0.25 serves /metrics from its own Registry, not
	// prometheus.DefaultRegisterer. Registering there would make the zeus_*
	// metrics invisible to the metrics endpoint.
	crmetrics.Registry.MustRegister(ReconcileErrors, RunbookPhase)
	// Publish a zero-valued child so the metric family is always exported,
	// even before the first reconcile error occurs.
	ReconcileErrors.WithLabelValues("startup")
}
