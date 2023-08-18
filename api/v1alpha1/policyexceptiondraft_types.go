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
	kyvernov2beta1 "github.com/kyverno/kyverno/api/kyverno/v2beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// PolicyExceptionDraftSpec defines the desired state of PolicyExceptionDraft
type PolicyExceptionDraftSpec struct {
	// Match defines match clause used to check if a resource applies to the exception
	Match kyvernov2beta1.MatchResources `json:"match"`

	// Exceptions is a list policy/rules to be excluded
	Exceptions []Exception `json:"exceptions"`
}

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status

// PolicyExceptionDraft is the Schema for the policyexceptiondrafts API
type PolicyExceptionDraft struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec PolicyExceptionDraftSpec `json:"spec,omitempty"`
}

// Exception stores infos about a policy and rules
type Exception struct {
	// PolicyName identifies the policy to which the exception is applied.
	// The policy name uses the format <namespace>/<name> unless it
	// references a ClusterPolicy.
	PolicyName string `json:"policyName"`

	// RuleNames identifies the rules to which the exception is applied.
	RuleNames []string `json:"ruleNames"`
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
