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
)

// PolicyManifestSpec defines the desired state of PolicyManifest
type PolicyManifestSpec struct {
	// Foo is an example field of PolicyManifest. Edit policymanifest_types.go to remove/update
	Mode                string            `json:"mode"`
	Args                []string          `json:"args"`
	Exceptions          []gsPolicy.Target `json:"exceptions"`
	AutomatedExceptions []gsPolicy.Target `json:"automatedExceptions"`
}

//+kubebuilder:object:root=true
//+kubebuilder:resource:shortName=polman,scope=Cluster
//+kubebuilder:object:root=true
//+kubebuilder:subresource:status

// PolicyManifest is the Schema for the policymanifests API
type PolicyManifest struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec PolicyManifestSpec `json:"spec,omitempty"`
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
