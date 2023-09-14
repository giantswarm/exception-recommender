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
	strings "strings"

	"github.com/go-logr/logr"
	policyreport "github.com/kyverno/kyverno/api/policyreport/v1alpha2"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	giantswarm "github.com/giantswarm/exception-recommender/api/v1alpha1"
)

// PolicyReportReconciler reconciles a PolicyReport object
type PolicyReportReconciler struct {
	client.Client
	Scheme               *runtime.Scheme
	Log                  logr.Logger
	DestinationNamespace string
	TargetWorkloads      []string
	TargetCategories     []string
	FailedReports        map[string]map[string][]policyreport.PolicyReportResult
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
		// Check if the report was deleted
		if apierrors.IsNotFound(err) {
			// Ignore
			return ctrl.Result{}, nil
		}

		// Error fetching the report
		log.Log.Error(err, "unable to fetch PolicyReport")
		return ctrl.Result{}, client.IgnoreNotFound(err)
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
					if result.Result == "fail" {
						// Logic to append the result into a map
						// Check for namespace in map
						if _, exists := r.FailedReports[policyReport.Namespace]; !exists {
							// It doesn't exist, create it
							r.FailedReports[policyReport.Namespace] = make(map[string][]policyreport.PolicyReportResult)
						}
						// Check for resource in map
						if _, exists := r.FailedReports[policyReport.Namespace][resource.Name]; !exists {
							// It doesn't exist, create it
							r.FailedReports[policyReport.Namespace][resource.Name] = []policyreport.PolicyReportResult{result}
							// Also create PolicyExceptionDraft
							polexDraft := createPolexDraft(result, namespace)
							log.Log.Info(fmt.Sprintf("Creating PolicyExceptionDraft for %s: %s/%s ", resource.Kind, resource.Namespace, resource.Name))
							if err := r.Create(ctx, &polexDraft); err != nil {
								if apierrors.IsAlreadyExists(err) {
									log.Log.Info(fmt.Sprintf("PolicyExceptionDraft %s/%s already exists", resource.Namespace, resource.Name))
									return ctrl.Result{}, nil
								} else {
									log.Log.Error(err, "unable to create PolicyExceptionDraft")
								}
							}
						} else {
							// It exists, check if we need to update the object
							if resultIsNotPresent(result, r.FailedReports[policyReport.Namespace][resource.Name]) {
								// Update map
								r.FailedReports[policyReport.Namespace][resource.Name] = append(r.FailedReports[policyReport.Namespace][resource.Name], result)
								// Regenerate Exceptions
								exceptions := generateExceptions(r.FailedReports[policyReport.Namespace][resource.Name])
								// Update PolicyExceptionDraft
								var polexDraft giantswarm.PolicyExceptionDraft
								if err := r.Get(ctx, types.NamespacedName{Namespace: namespace, Name: resource.Name}, &polexDraft); err != nil {
									// Error fetching the report
									log.Log.Error(err, "unable to fetch PolicyExceptionDraft")
									return ctrl.Result{}, client.IgnoreNotFound(err)
								}
								// Update PolicyExceptionDraft Exceptions
								polexDraft.Spec.Exceptions = exceptions
								// Update Kubernetes object
								if err := r.Client.Update(ctx, &polexDraft, &client.UpdateOptions{}); err != nil {
									r.Log.Error(err, "unable to update PolicyExceptionDraft")
								}
								log.Log.Info(fmt.Sprintf("Appending fail result to PolicyExceptionDraft: %s/%s", polexDraft.Namespace, polexDraft.Name))
							}
						}

					} else {
						// Pass result, edit or delete PolicyExceptionDraft
						if _, exists := r.FailedReports[policyReport.Namespace]; exists {
							// Namespace exists, check if resource exists too
							if _, exists := r.FailedReports[policyReport.Namespace][resource.Name]; exists {
								// Resource exists, check if the result is present in the failed reports
								if !resultIsNotPresent(result, r.FailedReports[policyReport.Namespace][resource.Name]) {
									// Resource was previously failing, remove it
									r.FailedReports[policyReport.Namespace][resource.Name] = removeResult(result, r.FailedReports[policyReport.Namespace][resource.Name])
									// Recreate exceptions
									exceptions := generateExceptions(r.FailedReports[policyReport.Namespace][resource.Name])

									// Get original PolicyExceptionDraft
									var polexDraft giantswarm.PolicyExceptionDraft
									if err := r.Get(ctx, types.NamespacedName{Namespace: namespace, Name: resource.Name}, &polexDraft); err != nil {
										// Error fetching the report
										log.Log.Error(err, "unable to fetch PolicyExceptionDraft")
										return ctrl.Result{}, client.IgnoreNotFound(err)
									}

									// Check if we need to delete the original PolicyExceptionDraft
									if len(exceptions) == 0 {
										// Delete Kubernetes object
										if err := r.Client.Delete(ctx, &polexDraft, &client.DeleteOptions{}); err != nil {
											r.Log.Error(err, "unable to update PolicyExceptionDraft")
										}
										log.Log.Info(fmt.Sprintf("Deleting PolicyExceptionDraft %s/%s because it doesn't have any fail results", polexDraft.Namespace, polexDraft.Name))
									} else {
										// Update PolicyExceptionDraft Exceptions
										polexDraft.Spec.Exceptions = exceptions
										// Update Kubernetes object
										if err := r.Client.Update(ctx, &polexDraft, &client.UpdateOptions{}); err != nil {
											r.Log.Error(err, "unable to update PolicyExceptionDraft")
										}
										log.Log.Info(fmt.Sprintf("Removing pass result from PolicyExceptionDraft %s/%s", polexDraft.Namespace, polexDraft.Name))

									}
								}
							}
						}
					}
				}
			}
		}
	}

	return ctrl.Result{}, nil
}

// Remove function to remove a result from an array
func removeResult(result policyreport.PolicyReportResult, array []policyreport.PolicyReportResult) []policyreport.PolicyReportResult {
	// Special case
	if len(array) == 1 {
		// Single element array, just return empty array
		return []policyreport.PolicyReportResult{}
	}
	for index, item := range array {
		if item.Rule == result.Rule {
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

func Substring(str string, start, end int) string {
	// Substring function to get a substring from the string
	return strings.TrimSpace(str[start:end])
}

func generateExceptions(results []policyreport.PolicyReportResult) []giantswarm.Exception {
	// Creates the Spec.Exceptions object for PolicyException(Draft) CRD.
	var exceptions []giantswarm.Exception
	for _, result := range results {
		ruleName := result.Rule
		if Substring(ruleName, 0, 8) == "autogen-" {
			ruleName = Substring(ruleName, 8, len(ruleName))
		}
		exceptions = append(exceptions, giantswarm.Exception{
			PolicyName: result.Policy,
			RuleNames:  []string{ruleName, "autogen-" + ruleName},
		})
	}
	return exceptions
}

func createPolexDraft(result policyreport.PolicyReportResult, destinationNamespace string) giantswarm.PolicyExceptionDraft {
	var polexDraft giantswarm.PolicyExceptionDraft
	for _, resource := range result.Resources {
		// Set namespace
		polexDraft.Namespace = destinationNamespace

		// Set name
		polexDraft.Name = resource.Name

		// Set Spec.Match
		polexDraft.Spec.Match = giantswarm.ResourceFilter{
			Namespaces: []string{resource.Namespace},
			Names:      []string{resource.Name + "*"},
			Kinds:      generateExceptionKinds(resource.Kind),
		}

		// Set Spec.Exceptions
		polexDraft.Spec.Exceptions = generateExceptions([]policyreport.PolicyReportResult{result})
	}

	return polexDraft
}

func resultIsNotPresent(result policyreport.PolicyReportResult, failedResults []policyreport.PolicyReportResult) bool {
	for _, failedResult := range failedResults {
		if failedResult.Rule == result.Rule {
			// Already exists, return false
			return false
		}
	}
	return true
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

func generateExceptionKinds(resourceKind string) []string {
	// Adds the subresources to the exception list for each Kind
	var exceptionKinds []string
	exceptionKinds = append(exceptionKinds, resourceKind)
	// Append ReplicaSets
	if resourceKind == "Deployment" {
		exceptionKinds = append(exceptionKinds, "ReplicaSet")
		// Append Jobs
	} else if resourceKind == "CronJob" {
		exceptionKinds = append(exceptionKinds, "Job")
	}
	// Always append Pods except if they are the initial resource Kind
	if resourceKind != "Pod" {
		exceptionKinds = append(exceptionKinds, "Pod")
	}

	return exceptionKinds
}

// SetupWithManager sets up the controller with the Manager.
func (r *PolicyReportReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		// Uncomment the following line adding a pointer to an instance of the controlled resource as an argument
		For(&policyreport.PolicyReport{}).
		Complete(r)
}
