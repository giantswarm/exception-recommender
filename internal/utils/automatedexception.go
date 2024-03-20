package exceptionutils

import (
	"strings"

	gsPolicy "github.com/giantswarm/kyverno-policy-operator/api/v1alpha1"
	policyreport "github.com/kyverno/kyverno/api/policyreport/v1alpha2"
	corev1 "k8s.io/api/core/v1"

	"github.com/giantswarm/exception-recommender/api/v1alpha1"
)

const (
	ComponentName      = "exception-recommender"
	AppLabelName       = "app.kubernetes.io/name"
	KindLabelName      = "policy.giantswarm.io/resource-kind"
	NamespaceLabelName = "policy.giantswarm.io/resource-namespace"
	NameLabelName      = "policy.giantswarm.io/resource-name"
)

func TemplateAutomatedException(policyReport policyreport.PolicyReport, failedPolicies []string, namespace string) v1alpha1.AutomatedException {
	// Template AutomatedException
	automatedException := v1alpha1.AutomatedException{}
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
