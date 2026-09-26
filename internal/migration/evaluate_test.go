package migration

import (
	"context"
	"errors"
	"strings"
	"testing"

	policyAPI "github.com/giantswarm/policy-api/api/v1alpha1"
	policiesv1 "github.com/kyverno/api/api/policies.kyverno.io/v1"
	kyvernov1 "github.com/kyverno/kyverno/api/kyverno/v1"
	kyvernov2 "github.com/kyverno/kyverno/api/kyverno/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
		require.NoError(t, add(s))
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
		clusterPolicy(nonrootPolicy, nonrootRule, nonrootAutogenRule),
		clusterPolicy(hostPathPolicy, hostPathRule, hostPathAutogenRule),
	}
	celOnly := []client.Object{validatingPolicy(nonrootPolicy), validatingPolicy(hostPathPolicy)}
	lossy := source(func(p *kyvernov2.PolicyException) { p.Spec.Exceptions[0].RuleNames = []string{nonrootRule} })
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
			// arrange
			c := fake.NewClientBuilder().WithScheme(testScheme(t)).WithObjects(tc.objects...).Build()

			// act
			got, err := Evaluate(context.Background(), c, bridgeNamespace, tc.src)

			// assert
			require.NoError(t, err)
			assert.Equal(t, tc.wantState, got.State)
			assert.Equal(t, tc.wantReason, got.Reason)
			assert.Equal(t, tc.wantWrite, got.Write)
		})
	}
}

func TestPolicyRulesIncludesAutogen(t *testing.T) {
	// arrange
	p := clusterPolicy(hostPathPolicy, hostPathRule)
	p.Status.Autogen.Rules = []kyvernov1.Rule{{Name: hostPathAutogenRule}}
	c := fake.NewClientBuilder().WithScheme(testScheme(t)).WithObjects(p).Build()

	// act
	got, found, err := PolicyRules(context.Background(), c)(hostPathPolicy)

	// assert
	require.NoError(t, err)
	assert.True(t, found)
	assert.Equal(t, []string{hostPathRule, hostPathAutogenRule}, got)
}

func TestPolicyRulesCELPolicies(t *testing.T) {
	meta := metav1.ObjectMeta{Name: hostPathPolicy}
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
			// arrange
			c := fake.NewClientBuilder().WithScheme(testScheme(t)).WithObjects(tc.objects...).WithInterceptorFuncs(tc.funcs).Build()

			// act
			got, found, err := PolicyRules(context.Background(), c)(hostPathPolicy)

			// assert
			require.NoError(t, err)
			assert.Equal(t, tc.wantFound, found)
			assert.Nil(t, got, "CEL policies have no rules")
		})
	}
}

// A lower-sorting exact source generated by kyverno-policy-operator is never bridged, so it must
// not claim the name.
func TestEvaluateSkipsKPOManagedLowerSource(t *testing.T) {
	// arrange
	kpo := inNamespace(source(nil), "default")
	kpo.Labels = map[string]string{ManagedByLabel: KPOComponentName}
	c := fake.NewClientBuilder().WithScheme(testScheme(t)).WithObjects(
		clusterPolicy(nonrootPolicy, nonrootRule, nonrootAutogenRule),
		clusterPolicy(hostPathPolicy, hostPathRule, hostPathAutogenRule),
		kpo,
	).Build()

	// act
	got, err := Evaluate(context.Background(), c, bridgeNamespace, source(nil))

	// assert
	require.NoError(t, err)
	assert.Equal(t, StatePending, got.State)
	assert.Equal(t, ReasonBridgeMissing, got.Reason)
	assert.True(t, got.Write)
}

func TestEvaluateNameTooLongSkipsBridgeLookup(t *testing.T) {
	// arrange
	src := source(func(p *kyvernov2.PolicyException) { p.Name = strings.Repeat("a", MaxNameLength) })
	failBridgeGet := interceptor.Funcs{Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
		if _, ok := obj.(*policyAPI.PolicyException); ok {
			return errors.New("bridge must not be looked up")
		}
		return c.Get(ctx, key, obj, opts...)
	}}
	c := fake.NewClientBuilder().WithScheme(testScheme(t)).WithInterceptorFuncs(failBridgeGet).Build()

	// act
	got, err := Evaluate(context.Background(), c, bridgeNamespace, src)

	// assert
	require.NoError(t, err)
	assert.Equal(t, StateUnsupported, got.State)
	assert.Equal(t, ReasonNameTooLong, got.Reason)
	assert.False(t, got.Write)
	assert.Nil(t, got.Bridge)
}

func TestEvaluateErrors(t *testing.T) {
	boom := errors.New("boom")
	policies := []client.Object{
		clusterPolicy(nonrootPolicy, nonrootRule, nonrootAutogenRule),
		clusterPolicy(hostPathPolicy, hostPathRule, hostPathAutogenRule),
	}
	failGet := func(match func(client.Object) bool) interceptor.Funcs {
		return interceptor.Funcs{Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
			if match(obj) {
				return boom
			}
			return c.Get(ctx, key, obj, opts...)
		}}
	}
	cases := map[string]struct {
		objects []client.Object
		funcs   interceptor.Funcs
	}{
		"policy lookup fails": {objects: policies, funcs: failGet(func(obj client.Object) bool {
			_, ok := obj.(*kyvernov1.ClusterPolicy)
			return ok
		})},
		"bridge lookup fails": {objects: policies, funcs: failGet(func(obj client.Object) bool {
			_, ok := obj.(*policyAPI.PolicyException)
			return ok
		})},
		"listing sources for the name claim fails": {objects: policies, funcs: interceptor.Funcs{
			List: func(ctx context.Context, c client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
				return boom
			},
		}},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			// arrange
			c := fake.NewClientBuilder().WithScheme(testScheme(t)).WithObjects(tc.objects...).WithInterceptorFuncs(tc.funcs).Build()

			// act
			_, err := Evaluate(context.Background(), c, bridgeNamespace, source(nil))

			// assert
			assert.ErrorIs(t, err, boom)
		})
	}
}

func TestPolicyRulesErrors(t *testing.T) {
	boom := errors.New("boom")
	cases := map[string]func(client.Object) bool{
		"ClusterPolicy lookup fails": func(obj client.Object) bool {
			_, ok := obj.(*kyvernov1.ClusterPolicy)
			return ok
		},
		"CEL policy lookup fails": func(obj client.Object) bool {
			_, ok := obj.(*policiesv1.MutatingPolicy)
			return ok
		},
	}
	for name, match := range cases {
		t.Run(name, func(t *testing.T) {
			// arrange
			c := fake.NewClientBuilder().WithScheme(testScheme(t)).WithInterceptorFuncs(interceptor.Funcs{
				Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
					if match(obj) {
						return boom
					}
					return c.Get(ctx, key, obj, opts...)
				},
			}).Build()

			// act
			_, found, err := PolicyRules(context.Background(), c)(hostPathPolicy)

			// assert
			assert.ErrorIs(t, err, boom)
			assert.False(t, found)
		})
	}
}
