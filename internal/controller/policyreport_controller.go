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
	"strings"

	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"

	policyreport "github.com/kyverno/kyverno/api/policyreport/v1alpha2"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	gsPolicy "github.com/giantswarm/kyverno-policy-operator/api/v1alpha1"

	"github.com/giantswarm/exception-recommender/api/v1alpha1"
	giantswarm "github.com/giantswarm/exception-recommender/api/v1alpha1"
)

const (
	ExceptionRecommenderFinalizer = "policy.giantswarm.io/exception-recommender"
	ManifestExpectedMode          = "warming"
	ComponentName                 = "exception-recommender"
	AppLabelName                  = "app.kubernetes.io/name"
	KindLabelName                 = "policy.giantswarm.io/resource-kind"
	NamespaceLabelName            = "policy.giantswarm.io/resource-namespace"
	NameLabelName                 = "policy.giantswarm.io/resource-name"
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
}

//+kubebuilder:rbac:groups=kyverno.io.giantswarm.io,resources=policyreports,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=kyverno.io.giantswarm.io,resources=policyreports/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=kyverno.io.giantswarm.io,resources=policyreports/finalizers,verbs=update

func (r *PolicyReportReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	_ = log.FromContext(ctx)
	_ = r.Log.WithValues("policyreport", req.NamespacedName)

	var policyReport policyreport.PolicyReport

	if err := r.Get(ctx, req.NamespacedName, &policyReport); err != nil {
		if !errors.IsNotFound(err) {
			// Error fetching the report
			log.Log.Error(err, "unable to fetch PolicyReport")
		}
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// Remove finalizers, we won't use them anymore
	if controllerutil.ContainsFinalizer(&policyReport, ExceptionRecommenderFinalizer) {
		controllerutil.RemoveFinalizer(&policyReport, ExceptionRecommenderFinalizer)
		// Update object
		if err := r.Update(ctx, &policyReport); err != nil {
			return reconcile.Result{}, err
		}
		// Exit unless the report is being deleted, we don't want duplicates from requeuing
		if policyReport.ObjectMeta.DeletionTimestamp.IsZero() {
			return reconcile.Result{}, nil
		}
	}

	// Ignore report if namespace is excluded
	if len(r.ExcludeNamespaces) != 0 {
		for _, namespace := range r.ExcludeNamespaces {
			if namespace == policyReport.Namespace {
				// Namespace is excluded, skip
				return reconcile.Result{}, nil
			}
		}
	}

	// Ignore report if kind is not part of TargetWorkloads
	if !isKind(policyReport.Scope.Kind, r.TargetWorkloads) {
		// Kind is not part of the targetWorkloads list, skip
		return reconcile.Result{}, nil
	}

	var failedPolicies []string

	for _, result := range policyReport.Results {
		// Check the result status and PolicyCategory
		if isPolicyCategory(result.Category, r.TargetCategories) {

			// Failed result, create or update AutomatedException
			if result.Result == "fail" {
				// Check if Policy is in warning mode or not
				log.Log.Info(fmt.Sprintf("Policy %s has failed for %s/%s", result.Policy, policyReport.Scope.Kind, policyReport.Scope.Name))
				var policyManifest giantswarm.PolicyManifest
				policyName := types.NamespacedName{Namespace: "", Name: result.Policy}

				if err := r.Get(ctx, policyName, &policyManifest); err != nil {
					if errors.IsNotFound(err) {
						// PolicyManifest not found, continue
						log.Log.Info(fmt.Sprintf("PolicyManifest %s not found, skipping", policyName.String()))
						continue
					}

					// Other errors
					log.Log.Error(err, "unable to fetch PolicyManifest")
					return ctrl.Result{}, client.IgnoreNotFound(err)
				}
				// Check Policy mode
				if policyManifest.Spec.Mode == ManifestExpectedMode {
					// Add it to the list of failed policies if it isn't already
					if !resultIsPresent(result.Policy, failedPolicies) {
						failedPolicies = append(failedPolicies, result.Policy)
					}
				}
			}
		}
	}

	var namespace string

	if r.DestinationNamespace == "" {
		namespace = policyReport.Scope.Namespace
	} else {
		namespace = r.DestinationNamespace
	}

	// Generate final Policy list
	if len(failedPolicies) != 0 {

		// Template AutomatedException
		automatedException := templateAutomatedException(policyReport, failedPolicies, namespace)

		// Create or Update AutomatedException
		c := Controller{r.Client}
		if op, err := c.CreateOrUpdate(ctx, &automatedException); err != nil {
			// Error creating or updating AutomatedException
			log.Log.Error(err, "unable to create or update AutomatedException")
			return ctrl.Result{}, client.IgnoreNotFound(err)
		} else {
			switch {
			case op == "create":
				log.Log.Info(fmt.Sprintf("Created AutomatedException %s/%s", automatedException.Namespace, automatedException.Name))
			case op == "update":
				log.Log.Info(fmt.Sprintf("Updated AutomatedException %s/%s", automatedException.Namespace, automatedException.Name))
			case op == "untouched":
				log.Log.Info(fmt.Sprintf("AutomatedException %s/%s is up to date", automatedException.Namespace, automatedException.Name))
			}
		}
	} else {
		// Get current draft and delete it
		// Delete AutomatedException
		automatedException := giantswarm.AutomatedException{
			ObjectMeta: ctrl.ObjectMeta{
				Name:      policyReport.Scope.Name + "-" + strings.ToLower(policyReport.Scope.Kind),
				Namespace: namespace,
			},
		}
		if err := r.Client.Delete(ctx, &automatedException, &client.DeleteOptions{}); err != nil {
			// Error deleting the AutomatedException
			r.Log.Error(err, "unable to delete AutomatedException")
			return ctrl.Result{}, client.IgnoreNotFound(err)
		} else {
			log.Log.Info(fmt.Sprintf("Deleted AutomatedException %s/%s because it doesn't have any failed results", automatedException.Namespace, automatedException.Name))
		}
	}

	return ctrl.Result{}, nil
}

// func unorderedEqual(want, got []string) bool {
// 	// Return false if lenghts are not the same
// 	if len(want) != len(got) {
// 		return false
// 	}
// 	// Create map
// 	exists := make(map[string]bool)
// 	for _, value := range want {
// 		exists[value] = true
// 	}
// 	// Compare values
// 	for _, value := range got {
// 		if !exists[value] {
// 			return false
// 		}
// 	}
// 	return true
// }

func templateAutomatedException(policyReport policyreport.PolicyReport, failedPolicies []string, namespace string) giantswarm.AutomatedException {
	// Template AutomatedException
	automatedException := giantswarm.AutomatedException{}
	// Set GrpupVersionKind
	automatedException.SetGroupVersionKind(v1alpha1.GroupVersion.WithKind("AutomatedException"))
	// Set Name
	automatedException.Name = policyReport.Scope.Name + "-" + strings.ToLower(policyReport.Scope.Kind)
	// Set Namespace
	automatedException.Namespace = namespace
	// Set Labels
	automatedException.Labels = generateLabels(*policyReport.Scope)
	// Set .Spec.Targets
	automatedException.Spec.Targets = generateTargets(*policyReport.Scope)
	// Set .Spec.Policies
	automatedException.Spec.Policies = failedPolicies

	return automatedException
}

func generateLabels(resource corev1.ObjectReference) map[string]string {
	labelMap := make(map[string]string)
	labelMap[AppLabelName] = ComponentName
	labelMap[NameLabelName] = resource.Name
	labelMap[NamespaceLabelName] = resource.Namespace
	labelMap[KindLabelName] = resource.Kind

	return labelMap
}

func generateTargets(resource corev1.ObjectReference) []gsPolicy.Target {
	var targets []gsPolicy.Target

	targets = append(targets, gsPolicy.Target{
		Namespaces: []string{resource.Namespace},
		Names:      []string{resource.Name + "*"},
		Kind:       resource.Kind,
	},
	)

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
