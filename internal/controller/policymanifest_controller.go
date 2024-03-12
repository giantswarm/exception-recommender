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

	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	gsPolicy "github.com/giantswarm/kyverno-policy-operator/api/v1alpha1"
	"github.com/go-logr/logr"

	policyv1alpha1 "github.com/giantswarm/exception-recommender/api/v1alpha1"
)

// PolicyManifestReconciler reconciles a PolicyManifest object
type PolicyManifestReconciler struct {
	client.Client
	Scheme               *runtime.Scheme
	Log                  logr.Logger
	DestinationNamespace string
}

//+kubebuilder:rbac:groups=policy.giantswarm.io,resources=policymanifests,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=policy.giantswarm.io,resources=policymanifests/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=policy.giantswarm.io,resources=policymanifests/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the PolicyManifest object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.15.0/pkg/reconcile
func (r *PolicyManifestReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	_ = log.FromContext(ctx)
	_ = r.Log.WithValues("policyreport", req.NamespacedName)

	var policyManifest policyv1alpha1.PolicyManifest

	if err := r.Get(ctx, req.NamespacedName, &policyManifest); err != nil {
		// Error fetching the report
		r.Log.Error(err, "unable to fetch PolicyManifest")
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// Check PolicyManifest current state
	if policyManifest.Spec.Mode == "warning" {
		if len(policyManifest.Spec.Exceptions) != 0 {
			// Create individual exceptions
			for _, exception := range policyManifest.Spec.Exceptions {
				// Create or Update the AutomatedException
				// TODO: Replace with AutomatedException when we have the CRD for it
				automatedException := gsPolicy.PolicyException{}
				// Set namespace
				var namespace string

				if r.DestinationNamespace == "" {
					namespace = exception.Namespaces[0]
				} else {
					namespace = r.DestinationNamespace
				}

				automatedException.Namespace = namespace
				// Remove wildcard from the name
				newName := strings.ReplaceAll(exception.Names[0], "*", "")
				automatedException.Name = newName + "-automated-exception"
				// We shouldn't have duplicated targets
				automatedException.Spec.Targets = append(automatedException.Spec.Targets, exception)
				// Set Policies
				// Should we map 1:1 PolicyManifest name and Policy name?
				automatedException.Spec.Policies = append(automatedException.Spec.Policies, policyManifest.Name)
				// Create or Update PolicyExceptionDraft
				if op, err := ctrl.CreateOrUpdate(ctx, r.Client, &automatedException, func() error {

					return nil
				}); err != nil {
					log.Log.Error(err, fmt.Sprintf("Reconciliation failed for AutomatedException %s", automatedException.Name))
				} else if op != "unchanged" {
					log.Log.Info(fmt.Sprintf("%s PolicyExceptionDraft %s/%s", op, automatedException.Namespace, automatedException.Name))
				}
			}
		} else {
			log.Log.Info("PolicyManifest has no exceptions declared")
		}
	}

	return reconcile.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *PolicyManifestReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&policyv1alpha1.PolicyManifest{}).
		Complete(r)
}
