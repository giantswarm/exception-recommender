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

	"github.com/giantswarm/exception-recommender/internal/testsupport"
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

// newClient returns a fake client holding objects.
func newClient(t *testing.T, objects ...client.Object) client.WithWatch {
	t.Helper()
	return fake.NewClientBuilder().WithScheme(testScheme(t)).WithObjects(objects...).Build()
}

// newClientWith returns a fake client holding objects whose calls go through funcs first.
func newClientWith(t *testing.T, funcs interceptor.Funcs, objects ...client.Object) client.WithWatch {
	t.Helper()
	return fake.NewClientBuilder().WithScheme(testScheme(t)).WithObjects(objects...).WithInterceptorFuncs(funcs).Build()
}

func clusterPolicy(name string, rules ...string) *kyvernov1.ClusterPolicy {
	p := &kyvernov1.ClusterPolicy{ObjectMeta: metav1.ObjectMeta{Name: name}}
	for _, r := range rules {
		p.Spec.Rules = append(p.Spec.Rules, kyvernov1.Rule{Name: r})
	}
	return p
}

// sourcePolicies are the ClusterPolicies source(nil) exempts, with exactly the rules it names, so
// that source(nil) translates exactly.
func sourcePolicies() []client.Object {
	return []client.Object{
		clusterPolicy(nonrootPolicy, nonrootRule, nonrootAutogenRule),
		clusterPolicy(hostPathPolicy, hostPathRule, hostPathAutogenRule),
	}
}

// sourceCELPolicies replace sourcePolicies with ValidatingPolicies of the same names.
func sourceCELPolicies() []client.Object {
	return []client.Object{
		&policiesv1.ValidatingPolicy{ObjectMeta: metav1.ObjectMeta{Name: nonrootPolicy}},
		&policiesv1.ValidatingPolicy{ObjectMeta: metav1.ObjectMeta{Name: hostPathPolicy}},
	}
}

// with returns objects followed by more, without changing objects.
func with(objects []client.Object, more ...client.Object) []client.Object {
	return append(append([]client.Object{}, objects...), more...)
}

// lossySource leaves out a rule of sourcePolicies; a bridge would exempt more than it does.
func lossySource() *kyvernov2.PolicyException {
	return source(func(p *kyvernov2.PolicyException) { p.Spec.Exceptions[0].RuleNames = []string{nonrootRule} })
}

// unsupportedSource selects by label, which a bridge cannot express.
func unsupportedSource() *kyvernov2.PolicyException {
	return source(func(p *kyvernov2.PolicyException) { p.Spec.Match.Any[0].Selector = &metav1.LabelSelector{} })
}

func inNamespace(src *kyvernov2.PolicyException, namespace string) *kyvernov2.PolicyException {
	src.Namespace = namespace
	return src
}

// bridgeOf is the bridge exception-recommender wrote for src.
func bridgeOf(src *kyvernov2.PolicyException) *policyAPI.PolicyException {
	bridge := handWrittenBridgeFor(src)
	bridge.Labels = map[string]string{ManagedByLabel: ComponentName}
	return bridge
}

// handWrittenBridgeFor looks like src's bridge but lacks the managed-by label: someone else's object.
func handWrittenBridgeFor(src *kyvernov2.PolicyException) *policyAPI.PolicyException {
	return &policyAPI.PolicyException{ObjectMeta: metav1.ObjectMeta{
		Name:        BridgeName(src),
		Namespace:   bridgeNamespace,
		Annotations: map[string]string{AnnotationMigratedFrom: SourceKey(src)},
	}}
}

func TestEvaluate(t *testing.T) {
	exact := source(nil) // giantswarm/cilium
	// "default/cilium" sorts before "giantswarm/cilium" and shares its bridge name.
	lower := func() *kyvernov2.PolicyException { return inNamespace(source(nil), "default") }
	cases := []struct {
		name       string
		src        *kyvernov2.PolicyException
		objects    []client.Object
		wantState  string
		wantReason string
		wantWrite  bool
	}{
		{name: "exact without bridge", src: exact, objects: sourcePolicies(),
			wantState: StatePending, wantReason: ReasonBridgeMissing, wantWrite: true},
		{name: "exact with own bridge", src: exact, objects: with(sourcePolicies(), bridgeOf(exact)),
			wantState: StateMigrated, wantWrite: true},
		{name: "name taken by a hand-written gspolex", src: exact, objects: with(sourcePolicies(), handWrittenBridgeFor(exact)),
			wantState: StateCollision, wantReason: ReasonNameTaken},
		{name: "name taken by the bridge of a source in another namespace", src: exact,
			objects:   with(sourcePolicies(), bridgeOf(inNamespace(source(nil), "other"))),
			wantState: StateCollision, wantReason: ReasonNameTaken},
		{name: "ClusterPolicy gone, ValidatingPolicy present, no bridge", src: exact, objects: sourceCELPolicies(),
			wantState: StatePending, wantReason: ReasonBridgeMissing, wantWrite: true},
		{name: "ClusterPolicy gone, ValidatingPolicy present, own bridge", src: exact,
			objects: with(sourceCELPolicies(), bridgeOf(exact)), wantState: StateMigrated, wantWrite: true},
		{name: "neither policy exists keeps the existing bridge", src: exact, objects: with(nil, bridgeOf(exact)),
			wantState: StateMigrated},
		{name: "neither policy exists and no bridge", src: exact,
			wantState: StatePending, wantReason: ReasonPolicyNotFound},
		{name: "lossy with own bridge keeps it", src: lossySource(), objects: with(sourcePolicies(), bridgeOf(lossySource())),
			wantState: StateLossy, wantReason: ReasonRuleNames},
		{name: "unsupported with own bridge keeps it", src: unsupportedSource(),
			objects:   with(sourcePolicies(), bridgeOf(unsupportedSource())),
			wantState: StateUnsupported, wantReason: ReasonSelector},
		{name: "lower-sorting exact source claims the name while no bridge exists", src: exact,
			objects: with(sourcePolicies(), lower()), wantState: StateCollision, wantReason: ReasonNameTaken},
		{name: "lowest-sorting source writes the bridge", src: lower(), objects: with(sourcePolicies(), exact),
			wantState: StatePending, wantReason: ReasonBridgeMissing, wantWrite: true},
		{name: "higher-sorting source keeps the bridge it already owns", src: exact,
			objects: with(sourcePolicies(), lower(), bridgeOf(exact)), wantState: StateMigrated, wantWrite: true},
		{name: "lower-sorting source does not take over an existing bridge", src: lower(),
			objects: with(sourcePolicies(), exact, bridgeOf(exact)), wantState: StateCollision, wantReason: ReasonNameTaken},
		{name: "lower-sorting source that is not exact does not claim the name", src: exact,
			objects:   with(sourcePolicies(), inNamespace(lossySource(), "default")),
			wantState: StatePending, wantReason: ReasonBridgeMissing, wantWrite: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// arrange
			c := newClient(t, tc.objects...)

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
	c := newClient(t, p)

	// act
	got, found, err := PolicyRules(context.Background(), c)(hostPathPolicy)

	// assert
	require.NoError(t, err)
	assert.True(t, found)
	assert.Equal(t, []string{hostPathRule, hostPathAutogenRule}, got)
}

func TestPolicyRulesCELPolicies(t *testing.T) {
	meta := metav1.ObjectMeta{Name: hostPathPolicy}
	// Only ClusterPolicies are served, as on a Kyverno without CEL policies.
	celKindsNotServed := interceptor.Funcs{Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
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
		"CEL policy kinds not served": {funcs: celKindsNotServed},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			// arrange
			c := newClientWith(t, tc.funcs, tc.objects...)

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
	c := newClient(t, with(sourcePolicies(), kpo)...)

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
	c := newClientWith(t, testsupport.FailGet[*policyAPI.PolicyException](errors.New("bridge must not be looked up")))

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
	cases := map[string]interceptor.Funcs{
		"policy lookup fails":                      testsupport.FailGet[*kyvernov1.ClusterPolicy](testsupport.ErrBoom),
		"bridge lookup fails":                      testsupport.FailGet[*policyAPI.PolicyException](testsupport.ErrBoom),
		"listing sources for the name claim fails": testsupport.FailList(testsupport.ErrBoom),
	}
	for name, funcs := range cases {
		t.Run(name, func(t *testing.T) {
			// arrange
			c := newClientWith(t, funcs, sourcePolicies()...)

			// act
			_, err := Evaluate(context.Background(), c, bridgeNamespace, source(nil))

			// assert
			assert.ErrorIs(t, err, testsupport.ErrBoom)
		})
	}
}

func TestPolicyRulesErrors(t *testing.T) {
	cases := map[string]interceptor.Funcs{
		"ClusterPolicy lookup fails": testsupport.FailGet[*kyvernov1.ClusterPolicy](testsupport.ErrBoom),
		"CEL policy lookup fails":    testsupport.FailGet[*policiesv1.MutatingPolicy](testsupport.ErrBoom),
	}
	for name, funcs := range cases {
		t.Run(name, func(t *testing.T) {
			// arrange
			c := newClientWith(t, funcs)

			// act
			_, found, err := PolicyRules(context.Background(), c)(hostPathPolicy)

			// assert
			assert.ErrorIs(t, err, testsupport.ErrBoom)
			assert.False(t, found)
		})
	}
}
