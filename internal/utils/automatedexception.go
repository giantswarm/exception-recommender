package utils

import (
	policyreport "github.com/kyverno/kyverno/api/policyreport/v1alpha2"
	corev1 "k8s.io/api/core/v1"

	policyAPI "github.com/giantswarm/policy-api/api/v1alpha1"
)

const (
	ComponentName      = "exception-recommender"
	AppLabelName       = "app.kubernetes.io/name"
	KindLabelName      = "policy.giantswarm.io/resource-kind"
	NamespaceLabelName = "policy.giantswarm.io/resource-namespace"
	NameLabelName      = "policy.giantswarm.io/resource-name"
)

// AutomatedExceptionName returns the AutomatedException name for policyReport.
// Create and delete both need it and they have to agree.
func AutomatedExceptionName(policyReport policyreport.PolicyReport) string {
	return string(policyReport.Scope.UID)
}

func TemplateAutomatedException(policyReport policyreport.PolicyReport, failedPolicies []string, namespace string) policyAPI.AutomatedException {
	// Template AutomatedException
	automatedException := policyAPI.AutomatedException{}
	// Set GroupVersionKind
	automatedException.SetGroupVersionKind(policyAPI.GroupVersion.WithKind("AutomatedException"))
	// Set resource UID as Name
	automatedException.Name = AutomatedExceptionName(policyReport)
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

func generateTargets(resource corev1.ObjectReference) []policyAPI.Target {
	var targets []policyAPI.Target

	targets = append(targets, policyAPI.Target{
		Namespaces: []string{resource.Namespace},
		Names:      []string{resource.Name},
		Kind:       resource.Kind,
	},
	)

	return targets
}
