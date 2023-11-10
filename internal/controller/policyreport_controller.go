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

	"github.com/go-logr/logr"
	policyreport "github.com/kyverno/kyverno/api/policyreport/v1alpha2"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	giantswarm "github.com/giantswarm/exception-recommender/api/v1alpha1"
	gsPolicy "github.com/giantswarm/kyverno-policy-operator/api/v1alpha1"
)

const (
	ExceptionRecommenderFinalizer = "policy.giantswarm.io/exception-recommender"
)

// PolicyReportReconciler reconciles a PolicyReport object
type PolicyReportReconciler struct {
	client.Client
	Scheme               *runtime.Scheme
	Log                  logr.Logger
	ExcludeNamespaces    []string
	DestinationNamespace string
	TargetWorkloads      []string
	TargetCategories     []string
	FailedReports        map[string]map[string]map[string][]string
}

//+kubebuilder:rbac:groups=kyverno.io.giantswarm.io,resources=policyreports,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=kyverno.io.giantswarm.io,resources=policyreports/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=kyverno.io.giantswarm.io,resources=policyreports/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the PolicyReport object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.15.0/pkg/reconcile
func (r *PolicyReportReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	_ = log.FromContext(ctx)
	_ = r.Log.WithValues("policyreport", req.NamespacedName)

	var policyReport policyreport.PolicyReport

	if err := r.Get(ctx, req.NamespacedName, &policyReport); err != nil {
		// Error fetching the report
		r.Log.Error(err, "unable to fetch PolicyReport")
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// Ignore report if namespace is excluded
	if len(r.ExcludeNamespaces) != 0 {
		for _, namespace := range r.ExcludeNamespaces {
			if namespace == policyReport.Namespace {
				// Namespace is excluded, ignore
				return reconcile.Result{}, nil
			}
		}
	}

	for _, result := range policyReport.Results {
		// Check the result status and PolicyCategory
		if isPolicyCategory(result.Category, r.TargetCategories) {
			for _, resource := range result.Resources {
				var namespace string

				if r.DestinationNamespace == "" {
					namespace = resource.Namespace
				} else {
					namespace = r.DestinationNamespace
				}
				// Inspec the resource kind
				if isKind(resource.Kind, r.TargetWorkloads) {
					// Failed result, create or update PolicyExceptionDraft
					if (result.Result == "fail" || result.Result == "skip") && policyReport.ObjectMeta.DeletionTimestamp.IsZero() {
						// Logic to append the result into a map
						// Check for namespace in map
						if _, exists := r.FailedReports[policyReport.Namespace]; !exists {
							// It doesn't exist, create it
							r.FailedReports[policyReport.Namespace] = make(map[string]map[string][]string)
						}
						// Check for resource in map
						if _, exists := r.FailedReports[policyReport.Namespace][resource.Name]; !exists {
							// It doesn't exist, create it
							// TODO: Instead of directly creating it, fetch the drafts and look if it already exists to create a cache
							r.FailedReports[policyReport.Namespace][resource.Name] = make(map[string][]string)
							r.FailedReports[policyReport.Namespace][resource.Name][result.Policy] = []string{result.Rule}
						} else {
							// It exists, check if we need to update the object
							if !resultIsPresent(result.Rule, r.FailedReports[policyReport.Namespace][resource.Name][result.Policy]) {
								// Update map
								r.FailedReports[policyReport.Namespace][resource.Name][result.Policy] = append(r.FailedReports[policyReport.Namespace][resource.Name][result.Policy], result.Rule)
							}
						}
					} else if result.Result == "pass" || !policyReport.ObjectMeta.DeletionTimestamp.IsZero() {
						// Resource exists, check if the result is present in the failed reports
						if _, exists := r.FailedReports[policyReport.Namespace][resource.Name][result.Policy]; exists {

							if resultIsPresent(result.Rule, r.FailedReports[policyReport.Namespace][resource.Name][result.Policy]) {
								// Resource was previously failing, remove it
								newRules := removeResult(result, r.FailedReports[policyReport.Namespace][resource.Name][result.Policy])
								if len(newRules) == 0 {
									delete(r.FailedReports[policyReport.Namespace][resource.Name], result.Policy)
								} else {
									r.FailedReports[policyReport.Namespace][resource.Name][result.Policy] = newRules
								}

								// Create new policy list and check if we need to remove the PolexDraft
								if len(generatePolicies(r.FailedReports[policyReport.Namespace][resource.Name])) == 0 {
									// Delete PolicyExceptionDraft
									polexDraft := giantswarm.PolicyExceptionDraft{
										ObjectMeta: ctrl.ObjectMeta{
											Name:      resource.Name,
											Namespace: namespace,
										},
									}
									if err := r.Client.Delete(ctx, &polexDraft, &client.DeleteOptions{}); err != nil {
										// Error deleting the PolicyExceptionDraft
										r.Log.Error(err, "unable to delete PolicyExceptionDraft")
										return ctrl.Result{}, client.IgnoreNotFound(err)
									} else {
										log.Log.Info(fmt.Sprintf("Deleted PolicyExceptionDraft %s/%s because it doesn't have any fail results", polexDraft.Namespace, polexDraft.Name))
									}
								}
							}
						}

					}
					// Generate final Policy list
					policies := generatePolicies(r.FailedReports[policyReport.Namespace][resource.Name])
					if len(policies) != 0 {
						// Template PolicyExceptionDraft
						polexDraft := giantswarm.PolicyExceptionDraft{}
						// Set Name
						polexDraft.Name = resource.Name
						// Set Namespace
						polexDraft.Namespace = namespace
						// Set Labels
						polexDraft.Labels = make(map[string]string)
						polexDraft.Labels["app.kubernetes.io/managed-by"] = "exception-recommender"
						// Set .Spec.Targets
						polexDraft.Spec.Targets = generateTargets(result)

						// Create or Update PolicyExceptionDraft
						if op, err := ctrl.CreateOrUpdate(ctx, r.Client, &polexDraft, func() error {
							// Check if Policies changed
							if !unorderedEqual(polexDraft.Spec.Policies, policies) {
								// Set .Spec.Policies
								polexDraft.Spec.Policies = policies
							}

							return nil
						}); err != nil {
							log.Log.Error(err, fmt.Sprintf("Reconciliation failed for PolicyExceptionDraft %s", polexDraft.Name))
							return ctrl.Result{}, err
						} else if op != "unchanged" {
							log.Log.Info(fmt.Sprintf("%s PolicyExceptionDraft %s/%s", op, polexDraft.Namespace, polexDraft.Name))
						}
					}
				}
			}
		}
	}

	// Remove Finalizer if the report is being deleted
	if !policyReport.ObjectMeta.DeletionTimestamp.IsZero() {
		controllerutil.RemoveFinalizer(&policyReport, ExceptionRecommenderFinalizer)
		// Update object
		if err := r.Update(ctx, &policyReport); err != nil {
			return reconcile.Result{}, err
		}
	} else {
		// Report is not being deleted
		// Check if we have the finalizer present
		if !controllerutil.ContainsFinalizer(&policyReport, ExceptionRecommenderFinalizer) {
			// Add finalizer since we don't have it
			controllerutil.AddFinalizer(&policyReport, ExceptionRecommenderFinalizer)
			// Update object
			if err := r.Update(ctx, &policyReport); err != nil {
				return reconcile.Result{}, err
			}
		}
	}

	return ctrl.Result{}, nil
}

func unorderedEqual(want, got []string) bool {
	// Return false if lenghts are not the same
	if len(want) != len(got) {
		return false
	}
	// Create map
	exists := make(map[string]bool)
	for _, value := range want {
		exists[value] = true
	}
	// Compare values
	for _, value := range got {
		if !exists[value] {
			return false
		}
	}
	return true
}

// Remove function to remove a result from an array
func removeResult(result policyreport.PolicyReportResult, array []string) []string {
	// Special case
	if len(array) == 1 {
		// Single element array, just return empty array
		return []string{}
	}
	for index, item := range array {
		if item == result.Rule {
			// Check if the current index is the last item of the array
			if index != len(array) {
				// Copy last item to current position
				array[index] = array[len(array)-1]
			}
			// Return array without last item
			return array[:len(array)-1]
		}
	}
	// Item not found, return original array
	return array
}

func generatePolicies(results map[string][]string) []string {
	// Creates the Spec.Policies object for PolicyException(Draft) CRD.
	var policies []string
	for policyName := range results {
		policies = append(policies, policyName)
	}
	return policies
}

func generateTargets(result policyreport.PolicyReportResult) []gsPolicy.Target {
	var targets []gsPolicy.Target
	for _, resource := range result.Resources {

		targets = append(targets, gsPolicy.Target{
			Namespaces: []string{resource.Namespace},
			Names:      []string{resource.Name + "*"},
			Kind:       resource.Kind,
		},
		)
	}
	return targets
}

func resultIsPresent(result string, failedResults []string) bool {
	for _, failedResult := range failedResults {
		if failedResult == result {
			// Already exists, return true
			return true
		}
	}
	return false
}

func isKind(resourceKind string, targetWorloads []string) bool {
	// Checks if the resource matches the kind in targetWorkloads
	for _, kind := range targetWorloads {
		if resourceKind == kind {
			return true
		}
	}
	return false
}

func isPolicyCategory(resultCategory string, targetCategories []string) bool {
	// Checks if the result category matches the category in targetCategories
	for _, category := range targetCategories {
		if resultCategory == category {
			return true
		}
	}
	return false
}

// SetupWithManager sets up the controller with the Manager.
func (r *PolicyReportReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		// Uncomment the following line adding a pointer to an instance of the controlled resource as an argument
		For(&policyreport.PolicyReport{}).
		Complete(r)
}
