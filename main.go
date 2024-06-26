/*
Copyright 2023.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package main

import (
	"flag"
	"os"
	"strings"

	// Import all Kubernetes client auth plugins (e.g. Azure, GCP, OIDC, etc.)
	// to ensure that exec-entrypoint and run can make use of them.
	kyverno "github.com/kyverno/kyverno/api/policyreport/v1alpha2"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	_ "k8s.io/client-go/plugin/pkg/client/auth"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/metrics/server"

	policyAPI "github.com/giantswarm/policy-api/api/v1alpha1"

	"github.com/giantswarm/exception-recommender/internal/controller"
	//+kubebuilder:scaffold:imports
)

var (
	scheme   = runtime.NewScheme()
	setupLog = ctrl.Log.WithName("setup")
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))

	err := kyverno.AddToScheme(scheme)
	if err != nil {
		setupLog.Error(err, "unable to register kyverno schema")
	}

	utilruntime.Must(policyAPI.AddToScheme(scheme))
	//+kubebuilder:scaffold:scheme
}

func main() {
	var metricsAddr string
	var enableLeaderElection bool
	var probeAddr string
	var destinationNamespace string
	var targetWorkloads []string
	var targetCategories []string
	var excludeNamespaces []string
	var maxJitterPercent int
	policyManifestCache := make(map[string]policyAPI.PolicyManifest)

	// Flags
	flag.StringVar(&destinationNamespace, "destination-namespace", "", "The namespace where the PolicyExceptionDrafts will be created. Defaults to resource namespace.")
	flag.StringVar(&metricsAddr, "metrics-bind-address", ":8080", "The address the metric endpoint binds to.")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "The address the probe endpoint binds to.")
	flag.BoolVar(&enableLeaderElection, "leader-elect", false,
		"Enable leader election for controller manager. "+
			"Enabling this will ensure there is only one active controller manager.")
	opts := zap.Options{
		Development: true,
	}
	flag.Func("target-categories",
		"A comma-separated list of Kyverno Policy Categories to be included in the Draft generation. For example: 'Pod Security Standards'",
		func(input string) error {
			items := strings.Split(input, ",")

			targetCategories = append(targetCategories, items...)

			return nil
		})
	flag.Func("target-workloads",
		"A comma-separated list of workloads to be included in the Draft generation. For example: DaemonSet,Deployment",
		func(input string) error {
			items := strings.Split(input, ",")

			targetWorkloads = append(targetWorkloads, items...)

			return nil
		})
	flag.Func("exclude-namespaces",
		"A comma-separated list of namespaces to be excluded from draft generation.",
		func(input string) error {
			items := strings.Split(input, ",")

			excludeNamespaces = append(excludeNamespaces, items...)

			return nil
		})
	flag.IntVar(&maxJitterPercent, "max-jitter-percent", 10,
		"Spreads out re-queue interval of reports by +/- this amount to spread load.")
	opts.BindFlags(flag.CommandLine)
	flag.Parse()

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme:                 scheme,
		Metrics:                server.Options{BindAddress: metricsAddr},
		HealthProbeBindAddress: probeAddr,
		LeaderElection:         enableLeaderElection,
		LeaderElectionID:       "24b79667.giantswarm.io",
	})
	if err != nil {
		setupLog.Error(err, "unable to start manager")
		os.Exit(1)
	}

	if err = (&controller.PolicyReportReconciler{
		Client:               mgr.GetClient(),
		Scheme:               mgr.GetScheme(),
		TargetWorkloads:      targetWorkloads,
		TargetCategories:     targetCategories,
		DestinationNamespace: destinationNamespace,
		ExcludeNamespaces:    excludeNamespaces,
		PolicyManifestCache:  policyManifestCache,
		MaxJitterPercent:     maxJitterPercent,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "PolicyReport")
		os.Exit(1)
	}
	if err = (&controller.PolicyManifestReconciler{
		Client:              mgr.GetClient(),
		Scheme:              mgr.GetScheme(),
		PolicyManifestCache: policyManifestCache,
		MaxJitterPercent:    maxJitterPercent,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "PolicyManifest")
		os.Exit(1)
	}
	//+kubebuilder:scaffold:builder

	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up health check")
		os.Exit(1)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up ready check")
		os.Exit(1)
	}

	setupLog.Info("starting manager")
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		setupLog.Error(err, "problem running manager")
		os.Exit(1)
	}
}
