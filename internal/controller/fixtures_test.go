package controller

import (
	"testing"

	policyAPI "github.com/giantswarm/policy-api/api/v1alpha1"
	policiesv1 "github.com/kyverno/api/api/policies.kyverno.io/v1"
	kyvernov1 "github.com/kyverno/kyverno/api/kyverno/v1"
	kyvernov2 "github.com/kyverno/kyverno/api/kyverno/v2"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"

	"github.com/giantswarm/exception-recommender/internal/migration"
)

const (
	testBridgeNamespace = "policy-exceptions"
	testPolicyName      = "require-run-as-nonroot"
	// stalePolicy is the policy of existingBridge, so a test can tell whether the bridge was rewritten.
	stalePolicy = "stale"
	kindPod     = "Pod"
)

var sourceKey = types.NamespacedName{Namespace: "giantswarm", Name: "cilium"}
var bridgeKey = types.NamespacedName{Namespace: testBridgeNamespace, Name: "cilium-migrated"}

func unitScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	for _, add := range []func(*runtime.Scheme) error{
		kyvernov1.Install, kyvernov2.Install, policiesv1.Install, policyAPI.AddToScheme, apiextensionsv1.AddToScheme,
	} {
		if err := add(s); err != nil {
			t.Fatal(err)
		}
	}
	return s
}

func legacyCRD() *apiextensionsv1.CustomResourceDefinition {
	return &apiextensionsv1.CustomResourceDefinition{
		ObjectMeta: metav1.ObjectMeta{Name: migration.LegacyCRDName},
		Spec: apiextensionsv1.CustomResourceDefinitionSpec{
			Versions: []apiextensionsv1.CustomResourceDefinitionVersion{{Name: "v2", Served: true, Storage: true}},
		},
	}
}

func nonrootPolicy() *kyvernov1.ClusterPolicy {
	p := &kyvernov1.ClusterPolicy{ObjectMeta: metav1.ObjectMeta{Name: testPolicyName}}
	p.Spec.Rules = []kyvernov1.Rule{{Name: "run-as-non-root"}, {Name: "autogen-run-as-non-root"}}
	return p
}

func legacySource(ruleNames ...string) *kyvernov2.PolicyException {
	src := &kyvernov2.PolicyException{ObjectMeta: metav1.ObjectMeta{Name: sourceKey.Name, Namespace: sourceKey.Namespace}}
	src.Spec.Exceptions = []kyvernov2.Exception{{PolicyName: testPolicyName, RuleNames: ruleNames}}
	src.Spec.Match.Any = kyvernov1.ResourceFilters{{ResourceDescription: kyvernov1.ResourceDescription{
		Kinds: []string{"DaemonSet", kindPod}, Namespaces: []string{"kube-system"}, Names: []string{"cilium*"},
	}}}
	return src
}

func existingBridge(labels map[string]string, source string) *policyAPI.PolicyException {
	return &policyAPI.PolicyException{
		ObjectMeta: metav1.ObjectMeta{Name: bridgeKey.Name, Namespace: bridgeKey.Namespace, Labels: labels,
			Annotations: map[string]string{migration.AnnotationMigratedFrom: source}},
		Spec: policyAPI.PolicyExceptionSpec{Policies: []string{stalePolicy}, Targets: []policyAPI.Target{}},
	}
}

var ownLabels = map[string]string{migration.ManagedByLabel: migration.ComponentName}

func named(src *kyvernov2.PolicyException, name string) *kyvernov2.PolicyException {
	src.Name = name
	return src
}
