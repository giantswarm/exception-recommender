package controller

import (
	"github.com/prometheus/client_golang/prometheus"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
)

var (
	PolrReconciliationFailures = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "exception_recommender_polr_reconciliation_failures",
			Help: "Number of failed PolicyReports reconciliation",
		},
	)
	PolmanReconciliationFailures = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "exception_recommender_polman_reconciliation_failures",
			Help: "Number of failed PolicyManifest reconciliation",
		},
	)
)

func init() {
	// Register custom metrics with the global prometheus registry
	metrics.Registry.MustRegister(PolrReconciliationFailures)
	metrics.Registry.MustRegister(PolmanReconciliationFailures)
}
