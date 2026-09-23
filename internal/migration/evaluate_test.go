package migration

import (
	"context"
	"testing"

	policyAPI "github.com/giantswarm/policy-api/api/v1alpha1"
	policiesv1 "github.com/kyverno/api/api/policies.kyverno.io/v1"
	kyvernov1 "github.com/kyverno/kyverno/api/kyverno/v1"
	kyvernov2 "github.com/kyverno/kyverno/api/kyverno/v2"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

const bridgeNamespace = "policy-exceptions"

func testScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	for _, add := range []func(*runtime.Scheme) error{kyvernov1.Install, kyvernov2.Install, policiesv1.Install, policyAPI.AddToScheme} {
		if err := add(s); err != nil {
			t.Fatal(err)
		}
	}
	return s
}

func clusterPolicy(name string, rules ...string) *kyvernov1.ClusterPolicy {
	p := &kyvernov1.ClusterPolicy{ObjectMeta: metav1.ObjectMeta{Name: name}}
	for _, r := range rules {
		p.Spec.Rules = append(p.Spec.Rules, kyvernov1.Rule{Name: r})
	}
	return p
}

func gspolex(name string, labels, annotations map[string]string) *policyAPI.PolicyException {
	return &policyAPI.PolicyException{ObjectMeta: metav1.ObjectMeta{
		Name: name, Namespace: bridgeNamespace, Labels: labels, Annotations: annotations,
	}}
}

func validatingPolicy(name string) *policiesv1.ValidatingPolicy {
	return &policiesv1.ValidatingPolicy{ObjectMeta: metav1.ObjectMeta{Name: name}}
}

func inNamespace(src *kyvernov2.PolicyException, namespace string) *kyvernov2.PolicyException {
	src.Namespace = namespace
	return src
}

func ownBridge(src *kyvernov2.PolicyException) *policyAPI.PolicyException {
	return gspolex(BridgeName(src), map[string]string{ManagedByLabel: ComponentName},
		map[string]string{AnnotationMigratedFrom: SourceKey(src)})
}

func TestEvaluate(t *testing.T) {
	policies := []client.Object{
		clusterPolicy("require-run-as-nonroot", "run-as-non-root", "autogen-run-as-non-root"),
		clusterPolicy("disallow-host-path", "host-path", "autogen-host-path"),
	}
	celOnly := []client.Object{validatingPolicy("require-run-as-nonroot"), validatingPolicy("disallow-host-path")}
	lossy := source(func(p *kyvernov2.PolicyException) { p.Spec.Exceptions[0].RuleNames = []string{"run-as-non-root"} })
	unsupported := source(func(p *kyvernov2.PolicyException) { p.Spec.Match.Any[0].Selector = &metav1.LabelSelector{} })
	// "default/cilium" sorts before "giantswarm/cilium", the namespace of source(nil).
	lower := func() *kyvernov2.PolicyException { return inNamespace(source(nil), "default") }
	cases := []struct {
		name       string
		src        *kyvernov2.PolicyException
		objects    []client.Object
		wantState  string
		wantReason string
		wantWrite  bool
	}{
		{name: "exact without bridge", src: source(nil), objects: policies,
			wantState: StatePending, wantReason: ReasonBridgeMissing, wantWrite: true},
		{name: "exact with own bridge", src: source(nil), objects: append(policies, ownBridge(source(nil))),
			wantState: StateMigrated, wantWrite: true},
		{name: "name taken by a hand-written gspolex", src: source(nil),
			objects:   append(policies, gspolex("cilium-migrated", nil, nil)),
			wantState: StateCollision, wantReason: ReasonNameTaken},
		{name: "name taken by the bridge of a source in another namespace", src: source(nil),
			objects: append(policies, gspolex("cilium-migrated", map[string]string{ManagedByLabel: ComponentName},
				map[string]string{AnnotationMigratedFrom: "other/cilium"})),
			wantState: StateCollision, wantReason: ReasonNameTaken},
		{name: "ClusterPolicy gone, ValidatingPolicy present, no bridge", src: source(nil), objects: celOnly,
			wantState: StatePending, wantReason: ReasonBridgeMissing, wantWrite: true},
		{name: "ClusterPolicy gone, ValidatingPolicy present, own bridge", src: source(nil),
			objects: append(celOnly, ownBridge(source(nil))), wantState: StateMigrated, wantWrite: true},
		{name: "neither policy exists keeps the existing bridge", src: source(nil), objects: []client.Object{ownBridge(source(nil))},
			wantState: StateMigrated},
		{name: "neither policy exists and no bridge", src: source(nil),
			wantState: StatePending, wantReason: ReasonPolicyNotFound},
		{name: "lossy with own bridge keeps it", src: lossy, objects: append(policies, ownBridge(lossy)),
			wantState: StateLossy, wantReason: ReasonRuleNames},
		{name: "unsupported with own bridge keeps it", src: unsupported, objects: append(policies, ownBridge(unsupported)),
			wantState: StateUnsupported, wantReason: ReasonSelector},
		{name: "lower-sorting exact source claims the name while no bridge exists", src: source(nil),
			objects: append(policies, lower()), wantState: StateCollision, wantReason: ReasonNameTaken},
		{name: "lowest-sorting source writes the bridge", src: lower(), objects: append(policies, source(nil)),
			wantState: StatePending, wantReason: ReasonBridgeMissing, wantWrite: true},
		{name: "higher-sorting source keeps the bridge it already owns", src: source(nil),
			objects: append(policies, lower(), ownBridge(source(nil))), wantState: StateMigrated, wantWrite: true},
		{name: "lower-sorting source does not take over an existing bridge", src: lower(),
			objects: append(policies, source(nil), ownBridge(source(nil))), wantState: StateCollision, wantReason: ReasonNameTaken},
		{name: "lower-sorting source that is not exact does not claim the name", src: source(nil),
			objects:   append(policies, inNamespace(lossy.DeepCopy(), "default")),
			wantState: StatePending, wantReason: ReasonBridgeMissing, wantWrite: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := fake.NewClientBuilder().WithScheme(testScheme(t)).WithObjects(tc.objects...).Build()
			got, err := Evaluate(context.Background(), c, bridgeNamespace, tc.src)
			if err != nil {
				t.Fatal(err)
			}
			if got.State != tc.wantState || got.Reason != tc.wantReason || got.Write != tc.wantWrite {
				t.Fatalf("got state %q reason %q write %v, want %q %q %v",
					got.State, got.Reason, got.Write, tc.wantState, tc.wantReason, tc.wantWrite)
			}
		})
	}
}

func TestPolicyRulesIncludesAutogen(t *testing.T) {
	p := clusterPolicy("disallow-host-path", "host-path")
	p.Status.Autogen.Rules = []kyvernov1.Rule{{Name: "autogen-host-path"}}
	c := fake.NewClientBuilder().WithScheme(testScheme(t)).WithObjects(p).Build()

	got, found, err := PolicyRules(context.Background(), c)("disallow-host-path")
	if err != nil || !found {
		t.Fatalf("got found %v err %v", found, err)
	}
	if len(got) != 2 || got[0] != "host-path" || got[1] != "autogen-host-path" {
		t.Fatalf("got %v", got)
	}
}

func TestPolicyRulesCELPolicies(t *testing.T) {
	meta := metav1.ObjectMeta{Name: "disallow-host-path"}
	noKind := interceptor.Funcs{Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
		if _, ok := obj.(*kyvernov1.ClusterPolicy); ok {
			return c.Get(ctx, key, obj, opts...)
		}
		return &apimeta.NoKindMatchError{}
	}}
	cases := map[string]struct {
		objects   []client.Object
		funcs     interceptor.Funcs
		wantFound bool
	}{
		"ValidatingPolicy":            {objects: []client.Object{&policiesv1.ValidatingPolicy{ObjectMeta: meta}}, wantFound: true},
		"MutatingPolicy":              {objects: []client.Object{&policiesv1.MutatingPolicy{ObjectMeta: meta}}, wantFound: true},
		"ImageValidatingPolicy":       {objects: []client.Object{&policiesv1.ImageValidatingPolicy{ObjectMeta: meta}}, wantFound: true},
		"no policy":                   {},
		"CEL policy kinds not served": {funcs: noKind},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			c := fake.NewClientBuilder().WithScheme(testScheme(t)).WithObjects(tc.objects...).WithInterceptorFuncs(tc.funcs).Build()
			got, found, err := PolicyRules(context.Background(), c)("disallow-host-path")
			if err != nil || found != tc.wantFound || got != nil {
				t.Fatalf("got rules %v found %v err %v, want no rules, found %v", got, found, err, tc.wantFound)
			}
		})
	}
}
