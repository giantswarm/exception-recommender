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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// PolicyExceptionDraftSpec defines the desired state of PolicyExceptionDraft
type PolicyExceptionDraftSpec struct {
	// Policies defines the list of policies to be excluded
	Policies []string `json:"policies"`

	// Targes defines the list of target workloads where the exceptions will be applied
	Targets []Target `json:"targets"`
}

//+kubebuilder:object:root=true
//+kubebuilder:resource:shortName=polexdraft
//+kubebuilder:subresource:status

// PolicyExceptionDraft is the Schema for the policyexceptiondrafts API
type PolicyExceptionDraft struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec PolicyExceptionDraftSpec `json:"spec,omitempty"`
}

// Target defines a resource to which a PolicyException applies
type Target struct {
	Namespaces []string `json:"namespaces"`
	Names      []string `json:"names"`
	Kind       string   `json:"kind"`
}

//+kubebuilder:object:root=true

// PolicyExceptionDraftList contains a list of PolicyExceptionDraft
type PolicyExceptionDraftList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []PolicyExceptionDraft `json:"items"`
}

func init() {
	SchemeBuilder.Register(&PolicyExceptionDraft{}, &PolicyExceptionDraftList{})
}
