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

package v1alpha1

import (
	gsPolicy "github.com/giantswarm/kyverno-policy-operator/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	// date
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// PolicyManifestSpec defines the desired state of PolicyManifest
type PolicyManifestSpec struct {
	// Foo is an example field of PolicyManifest. Edit policymanifest_types.go to remove/update
	Mode       string            `json:"mode"`
	Args       []string          `json:"args"`
	Exceptions []gsPolicy.Target `json:"exceptions"`
}

// PolicyManifestStatus defines the observed state of PolicyManifest
type PolicyManifestStatus struct {
	ExceptionReonciliation ExceptionRecommenderReconciliationStatus `json:"exceptionReconciliation,omitempty"`
}

// ExceptionReconciliationStatus defines the status of the Exception Recommender reconciliations
type ExceptionRecommenderReconciliationStatus struct {
	LastReconciliationTime string `json:"lastReconciliationTime"` // Should be date
}

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status

// PolicyManifest is the Schema for the policymanifests API
type PolicyManifest struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   PolicyManifestSpec   `json:"spec,omitempty"`
	Status PolicyManifestStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// PolicyManifestList contains a list of PolicyManifest
type PolicyManifestList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []PolicyManifest `json:"items"`
}

func init() {
	SchemeBuilder.Register(&PolicyManifest{}, &PolicyManifestList{})
}
