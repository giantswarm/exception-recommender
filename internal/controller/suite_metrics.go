package controller

import (
	"github.com/prometheus/client_golang/prometheus"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
)

var (
	ReconciliationFailuresMetric = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "exception_recommender_reconciliation_failures_total",
			Help: "Number of failed reconciliations",
		}, []string{"resource_type"},
	)
)

func init() {
	// Register custom metrics with the global prometheus registry
	metrics.Registry.MustRegister(ReconciliationFailuresMetric)
}
