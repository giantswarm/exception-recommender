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
	policyreport "github.com/kyverno/kyverno/api/policyreport/v1alpha2"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	gsPolicy "github.com/giantswarm/kyverno-policy-operator/api/v1alpha1"

	giantswarm "github.com/giantswarm/exception-recommender/api/v1alpha1"
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

	if !isKind(policyReport.Scope.Kind, r.TargetWorkloads) {
		// Kind is not part of the targetWorkloads list, skip
		return reconcile.Result{}, nil
	}

	var failedPolicies []string

	for _, result := range policyReport.Results {
		// Check the result status and PolicyCategory
		if isPolicyCategory(result.Category, r.TargetCategories) {

			// Failed result, create or update PolicyExceptionDraft
			if result.Result == "fail" {

				if !resultIsPresent(result.Policy, failedPolicies) {
					// Update map
					failedPolicies = append(failedPolicies, result.Policy)
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

		// Template PolicyExceptionDraft
		polexDraft := giantswarm.PolicyExceptionDraft{}
		// Set Name
		polexDraft.Name = policyReport.Scope.Name + "-" + strings.ToLower(policyReport.Scope.Kind)
		// Set Namespace
		polexDraft.Namespace = namespace
		// Set Labels
		polexDraft.Labels = make(map[string]string)
		polexDraft.Labels["app.kubernetes.io/managed-by"] = "exception-recommender"
		polexDraft.Labels["policy.giantswarm.io/resource-name"] = policyReport.Scope.Name
		polexDraft.Labels["policy.giantswarm.io/resource-namespace"] = policyReport.Scope.Namespace
		polexDraft.Labels["policy.giantswarm.io/resource-kind"] = policyReport.Scope.Kind

		// Set .Spec.Targets
		polexDraft.Spec.Targets = generateTargets(*policyReport.Scope)

		// Create or Update PolicyExceptionDraft
		if op, err := ctrl.CreateOrUpdate(ctx, r.Client, &polexDraft, func() error {
			// Check if Policies changed
			if !unorderedEqual(polexDraft.Spec.Policies, failedPolicies) {
				// Set .Spec.Policies
				polexDraft.Spec.Policies = failedPolicies
			}
			return nil
		}); err != nil {
			log.Log.Error(err, fmt.Sprintf("Reconciliation failed for PolicyExceptionDraft %s", polexDraft.Name))
		} else if op != "unchanged" {
			log.Log.Info(fmt.Sprintf("%s PolicyExceptionDraft %s/%s", op, polexDraft.Namespace, polexDraft.Name))
		}
	} else {
		// Get current draft and delete it
		// Delete PolicyExceptionDraft
		polexDraft := giantswarm.PolicyExceptionDraft{
			ObjectMeta: ctrl.ObjectMeta{
				Name:      policyReport.Scope.Name + "-" + strings.ToLower(policyReport.Scope.Kind),
				Namespace: namespace,
			},
		}
		if err := r.Client.Delete(ctx, &polexDraft, &client.DeleteOptions{}); err != nil {
			// Error deleting the PolicyExceptionDraft
			r.Log.Error(err, "unable to delete PolicyExceptionDraft")
			return ctrl.Result{}, client.IgnoreNotFound(err)
		} else {
			log.Log.Info(fmt.Sprintf("Deleted PolicyExceptionDraft %s/%s because it doesn't have any failed results", polexDraft.Namespace, polexDraft.Name))
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
