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
	policiesv1 "github.com/kyverno/api/api/policies.kyverno.io/v1"
	kyvernov1 "github.com/kyverno/kyverno/api/kyverno/v1"
	kyvernov2 "github.com/kyverno/kyverno/api/kyverno/v2"
	kyverno "github.com/kyverno/kyverno/api/policyreport/v1alpha2"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	_ "k8s.io/client-go/plugin/pkg/client/auth"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
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

	err := kyverno.Install(scheme)
	if err != nil {
		setupLog.Error(err, "unable to register kyverno schema")
	}

	utilruntime.Must(policyAPI.AddToScheme(scheme))
	utilruntime.Must(kyvernov1.Install(scheme))
	utilruntime.Must(kyvernov2.Install(scheme))
	utilruntime.Must(policiesv1.Install(scheme))
	utilruntime.Must(apiextensionsv1.AddToScheme(scheme))
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
	var enableAutomatedExceptions bool
	var enableMigrationBridges bool
	var bridgeNamespace string
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
	flag.BoolVar(&enableAutomatedExceptions, "enable-automated-exceptions", false,
		"Create AutomatedExceptions from PolicyReport failures of policies whose PolicyManifest is in warming mode.")
	flag.BoolVar(&enableMigrationBridges, "enable-migration-bridges", true,
		"Write a Giant Swarm PolicyException for each legacy kyverno.io PolicyException that translates exactly. Disable where ER runs for another reason and bridging is not wanted.")
	flag.StringVar(&bridgeNamespace, "bridge-namespace", "policy-exceptions",
		"The namespace of the migration bridges. Must be kyverno-policy-operator's --destination-namespace.")
	opts.BindFlags(flag.CommandLine)
	flag.Parse()

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))

	cacheOptions := cache.Options{}
	if enableMigrationBridges {
		// Bridges live in one namespace; only cache Giant Swarm PolicyExceptions there.
		cacheOptions.ByObject = map[client.Object]cache.ByObject{
			&policyAPI.PolicyException{}: {Namespaces: map[string]cache.Config{bridgeNamespace: {}}},
		}
	}

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme:                 scheme,
		Cache:                  cacheOptions,
		Metrics:                server.Options{BindAddress: metricsAddr},
		HealthProbeBindAddress: probeAddr,
		LeaderElection:         enableLeaderElection,
		LeaderElectionID:       "24b79667.giantswarm.io",
	})
	if err != nil {
		setupLog.Error(err, "unable to start manager")
		os.Exit(1)
	}

	if enableAutomatedExceptions {
		setupLog.Info("automated exceptions enabled, starting the PolicyReport and PolicyManifest controllers")
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
	} else {
		setupLog.Info("automated exceptions disabled, not starting the PolicyReport and PolicyManifest controllers")
	}

	if enableMigrationBridges {
		setupMigrationBridges(mgr, bridgeNamespace)
	} else {
		setupLog.Info("migration bridges disabled")
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

// setupMigrationBridges registers the legacy PolicyException controller, its resync and its metrics.
// Without the legacy Kyverno CRDs (Kyverno 1.20) there is nothing to bridge, and no bridge is deleted.
func setupMigrationBridges(mgr ctrl.Manager, bridgeNamespace string) {
	present, err := controller.LegacyCRDsPresent(mgr.GetRESTMapper())
	if err != nil {
		setupLog.Error(err, "unable to check for legacy Kyverno CRDs")
		os.Exit(1)
	}
	if !present {
		setupLog.Info("legacy Kyverno CRDs not served, migration bridges disabled and existing bridges kept")
		return
	}

	resync := make(chan event.GenericEvent)
	if err := (&controller.LegacyExceptionReconciler{
		Client:          mgr.GetClient(),
		APIReader:       mgr.GetAPIReader(),
		BridgeNamespace: bridgeNamespace,
	}).SetupWithManager(mgr, resync); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "LegacyPolicyException")
		os.Exit(1)
	}
	if err := mgr.Add(&controller.Resyncer{
		APIReader: mgr.GetAPIReader(),
		Events:    resync,
		Interval:  controller.ResyncInterval,
		Log:       ctrl.Log.WithName("resync"),
	}); err != nil {
		setupLog.Error(err, "unable to add resync")
		os.Exit(1)
	}
	metrics.Registry.MustRegister(controller.BridgesRemoved, controller.TranslationErrors, controller.LastResync,
		&controller.MigrationCollector{Reader: mgr.GetClient(), BridgeNamespace: bridgeNamespace, Log: ctrl.Log.WithName("metrics")})
	setupLog.Info("migration bridges enabled", "namespace", bridgeNamespace)
}
