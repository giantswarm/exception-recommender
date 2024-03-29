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
	"reflect"

	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/giantswarm/exception-recommender/api/v1alpha1"
)

// PolicyManifestReconciler reconciles a PolicyManifest object
type PolicyManifestReconciler struct {
	client.Client
	Scheme              *runtime.Scheme
	Log                 logr.Logger
	PolicyManifestCache map[string]v1alpha1.PolicyManifest
}

func (r *PolicyManifestReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	_ = log.FromContext(ctx)
	_ = r.Log.WithValues("policymanifest", req.NamespacedName)

	var policyManifest v1alpha1.PolicyManifest

	if err := r.Get(ctx, req.NamespacedName, &policyManifest); err != nil {
		if !errors.IsNotFound(err) {
			// Error fetching the report
			log.Log.Error(err, "unable to fetch PolicyManifest")
		}
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// Delete manifest from cache if it is being deleted
	if !policyManifest.ObjectMeta.DeletionTimestamp.IsZero() {
		delete(r.PolicyManifestCache, policyManifest.Name)
	} else {
		// Add the PolicyManifest to the cache
		r.PolicyManifestCache[policyManifest.Name] = policyManifest
	}

	return ctrl.Result{}, nil
}

func GetPolicyManifestMode(policyName string, cache map[string]v1alpha1.PolicyManifest) string {
	// Get the PolicyManifest from the cache
	policyManifest := cache[policyName]

	// Check if PolicyManifest is not empty
	if reflect.DeepEqual(policyManifest, v1alpha1.PolicyManifest{}) {
		return ""
	}

	return policyManifest.Spec.Mode
}

// SetupWithManager sets up the controller with the Manager.
func (r *PolicyManifestReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.PolicyManifest{}).
		Complete(r)
}
