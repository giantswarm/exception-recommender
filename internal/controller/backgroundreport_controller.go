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

package controller

import (
	"context"
	"fmt"

	"github.com/giantswarm/exception-recommender/api/v1alpha1"
	"github.com/go-logr/logr"
	kyvernov1 "github.com/kyverno/kyverno/api/kyverno/v1"
	kyvernov1alpha2 "github.com/kyverno/kyverno/api/kyverno/v1alpha2"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// BackgroundScanReportReconciler reconciles a BackgroundScanReport object
type BackgroundScanReportReconciler struct {
	client.Client
	Scheme           *runtime.Scheme
	Log              logr.Logger
	TargetWorkloads  []string
	TargetCategories []string
}

//+kubebuilder:rbac:groups=kyverno.io.giantswarm.io,resources=backgroundscanreports,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=kyverno.io.giantswarm.io,resources=backgroundscanreports/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=kyverno.io.giantswarm.io,resources=backgroundscanreports/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the BackgroundScanReport object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.15.0/pkg/reconcile
func (r *BackgroundScanReportReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	_ = log.FromContext(ctx)
	_ = r.Log.WithValues("backgroundscanreport", req.NamespacedName)

	var backgroundReport kyvernov1alpha2.BackgroundScanReport

	if err := r.Get(ctx, req.NamespacedName, &backgroundReport); err != nil {
		// Check if the report was deleted
		if apierrors.IsNotFound(err) {
			// Ignore
			return ctrl.Result{}, nil
		}

		// Error fetching the report
		log.Log.Error(err, "unable to fetch BackgroundScanReport")
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	// Ignore if it has reconciled label
	if _, exists := backgroundReport.Labels["exception-recommender.reconciled"]; exists {
		return ctrl.Result{}, nil
	}
	// Add label
	backgroundReport.Labels["exception-recommender.reconciled"] = "true"
	err := r.Client.Update(ctx, &backgroundReport, &client.UpdateOptions{})
	if err != nil {
		r.Log.Error(err, "unable to add exception-recommender label")
	}
	if isKind(backgroundReport.OwnerReferences[0].Kind, r.TargetWorkloads) {
		log.Log.Info(fmt.Sprintf("BackgroundScanReport %s reconciled. Failed checks: %d", backgroundReport.Name, backgroundReport.Spec.Summary.Fail))
		if backgroundReport.Spec.Summary.Fail != 0 {
			// Report has failed checks

			// Define exceptions
			var exceptions []v1alpha1.Exception

			for _, result := range backgroundReport.Spec.Results {
				if result.Result == "fail" && isPolicyCategory(result.Category, r.TargetCategories) {
					// Only create exceptions for Policies that have the desired Category
					exceptions = append(exceptions, v1alpha1.Exception{
						PolicyName: result.Policy,
						RuleNames:  []string{result.Rule, "autogen-" + result.Rule},
					})
				}
			}
			if len(exceptions) > 0 {
				// Create Policy Exception Draft
				var polexDraft v1alpha1.PolicyExceptionDraft
				polexDraft.Name = backgroundReport.OwnerReferences[0].Name
				polexDraft.Namespace = backgroundReport.Namespace

				polexDraft.Spec.Match.All = kyvernov1.ResourceFilters{kyvernov1.ResourceFilter{
					ResourceDescription: kyvernov1.ResourceDescription{
						Namespaces: []string{backgroundReport.Namespace},
						Names:      []string{backgroundReport.OwnerReferences[0].Name + "*"},
						Kinds:      generateExceptionKinds(backgroundReport.OwnerReferences[0].Kind),
					}}}
				polexDraft.Spec.Exceptions = exceptions
				if err := r.Create(ctx, &polexDraft); err != nil {
					log.Log.Error(err, "unable to create PolicyExceptionDraft")
				}
			}

		}

	}

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *BackgroundScanReportReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		// Uncomment the following line adding a pointer to an instance of the controlled resource as an argument
		For(&kyvernov1alpha2.BackgroundScanReport{}).
		Complete(r)
}
