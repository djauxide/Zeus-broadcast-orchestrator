package controller

import "github.com/prometheus/client_golang/prometheus"

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
	prometheus.MustRegister(ReconcileErrors, RunbookPhase)
}
