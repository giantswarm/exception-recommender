package controller

import (
	"github.com/prometheus/client_golang/prometheus"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
)

var (
	RecommenderFailures = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "exception_recommender_failures",
			Help: "Number of failed reports reconciliation",
		},
	)
)

var MetricLabels = []string{
	"workload_name",
	"workload_kind",
	"workload_namespace",
	"failed_policy",
}

func init() {
	// Register custom metrics with the global prometheus registry
	metrics.Registry.MustRegister(RecommenderFailures)
}
