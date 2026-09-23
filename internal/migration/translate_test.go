package migration

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	policyAPI "github.com/giantswarm/policy-api/api/v1alpha1"
	kyvernov1 "github.com/kyverno/kyverno/api/kyverno/v1"
	kyvernov2 "github.com/kyverno/kyverno/api/kyverno/v2"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// rules is a RuleLookup backed by a map; a missing key is a policy that does not exist, and a nil
// value a CEL policy, which has no rules.
func rules(policies map[string][]string) RuleLookup {
	return func(name string) ([]string, bool, error) {
		r, ok := policies[name]
		return r, ok, nil
	}
}

var clusterPolicies = rules(map[string][]string{
	"require-run-as-nonroot": {"run-as-non-root", "autogen-run-as-non-root", "autogen-cronjob-run-as-non-root"},
	"disallow-host-path":     {"host-path", "autogen-host-path", "autogen-cronjob-host-path"},
})

// celPolicies has ValidatingPolicies of the same names and no ClusterPolicies.
var celOnly = rules(map[string][]string{"require-run-as-nonroot": nil, "disallow-host-path": nil})

func filter(kinds ...string) kyvernov1.ResourceFilter {
	return kyvernov1.ResourceFilter{ResourceDescription: kyvernov1.ResourceDescription{
		Kinds: kinds, Namespaces: []string{"kube-system"}, Names: []string{"cilium*"},
	}}
}

func source(mutate func(*kyvernov2.PolicyException)) *kyvernov2.PolicyException {
	src := &kyvernov2.PolicyException{
		ObjectMeta: metav1.ObjectMeta{Name: "cilium", Namespace: "giantswarm"},
		Spec: kyvernov2.PolicyExceptionSpec{
			Exceptions: []kyvernov2.Exception{
				{PolicyName: "require-run-as-nonroot", RuleNames: []string{"run-as-non-root", "autogen-run-as-non-root"}},
				{PolicyName: "disallow-host-path", RuleNames: []string{"host-path", "autogen-host-path"}},
			},
		},
	}
	src.Spec.Match.Any = kyvernov1.ResourceFilters{filter("DaemonSet", "Pod")}
	if mutate != nil {
		mutate(src)
	}
	return src
}

func TestTranslateExact(t *testing.T) {
	got, err := Translate(source(nil), clusterPolicies)
	if err != nil {
		t.Fatal(err)
	}
	want := Translation{Spec: policyAPI.PolicyExceptionSpec{
		Policies: []string{"require-run-as-nonroot", "disallow-host-path"},
		Targets: []policyAPI.Target{
			{Kind: "DaemonSet", Names: []string{"cilium*"}, Namespaces: []string{"kube-system"}},
			{Kind: "Pod", Names: []string{"cilium*"}, Namespaces: []string{"kube-system"}},
		},
	}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v\nwant %+v", got, want)
	}
}

func TestTranslateStates(t *testing.T) {
	longName := strings.Repeat("a", MaxNameLength-len(BridgeSuffix)+1)
	cases := []struct {
		name       string
		mutate     func(*kyvernov2.PolicyException)
		lookup     RuleLookup
		wantState  string
		wantReason string
	}{
		{name: "single match.all filter is exact", mutate: func(p *kyvernov2.PolicyException) {
			p.Spec.Match.All, p.Spec.Match.Any = p.Spec.Match.Any, nil
		}},
		{name: "deprecated name field is exact", mutate: func(p *kyvernov2.PolicyException) {
			p.Spec.Match.Any[0].Names, p.Spec.Match.Any[0].Name = nil, "cilium*"
		}},
		{name: "wildcard rule names skip the lookup", lookup: rules(nil), mutate: func(p *kyvernov2.PolicyException) {
			for i := range p.Spec.Exceptions {
				p.Spec.Exceptions[i].RuleNames = []string{"*"}
			}
		}},
		{name: "rule name patterns are globs", mutate: func(p *kyvernov2.PolicyException) {
			p.Spec.Exceptions[0].RuleNames = []string{"*run-as-non-root"}
		}},
		{name: "name at the limit is exact", mutate: func(p *kyvernov2.PolicyException) { p.Name = longName[1:] }},
		{name: "name too long", mutate: func(p *kyvernov2.PolicyException) { p.Name = longName },
			wantState: StateUnsupported, wantReason: ReasonNameTooLong},
		{name: "missing rule is lossy", mutate: func(p *kyvernov2.PolicyException) {
			p.Spec.Exceptions[0].RuleNames = []string{"run-as-non-root"}
		}, wantState: StateLossy, wantReason: ReasonRuleNames},
		{name: "empty rule names are lossy", mutate: func(p *kyvernov2.PolicyException) {
			p.Spec.Exceptions[0].RuleNames = nil
		}, wantState: StateLossy, wantReason: ReasonRuleNames},
		{name: "autogen-cronjob rules are required for CronJob targets", mutate: func(p *kyvernov2.PolicyException) {
			p.Spec.Match.Any = kyvernov1.ResourceFilters{filter("CronJob", "Job", "Pod")}
		}, wantState: StateLossy, wantReason: ReasonRuleNames},
		{name: "CEL policy without ClusterPolicy is exact", lookup: celOnly},
		{name: "CEL policy without ClusterPolicy ignores rule names", lookup: celOnly, mutate: func(p *kyvernov2.PolicyException) {
			p.Spec.Exceptions[0].RuleNames = nil
		}},
		{name: "neither ClusterPolicy nor CEL policy is pending", lookup: rules(nil),
			wantState: StatePending, wantReason: ReasonPolicyNotFound},
		{name: "unsupported beats lossy", mutate: func(p *kyvernov2.PolicyException) {
			p.Spec.Exceptions[0].RuleNames = nil
			p.Spec.PodSecurity = []kyvernov1.PodSecurityStandard{{ControlName: "Capabilities"}}
		}, wantState: StateUnsupported, wantReason: ReasonPodSecurity},
		{name: "conditions", mutate: func(p *kyvernov2.PolicyException) { p.Spec.Conditions = &kyvernov2.AnyAllConditions{} },
			wantState: StateUnsupported, wantReason: ReasonConditions},
		{name: "no match", mutate: func(p *kyvernov2.PolicyException) { p.Spec.Match.Any = nil },
			wantState: StateUnsupported, wantReason: ReasonNoMatch},
		{name: "match.all with two filters", mutate: func(p *kyvernov2.PolicyException) {
			p.Spec.Match.All, p.Spec.Match.Any = kyvernov1.ResourceFilters{filter("Pod"), filter("DaemonSet")}, nil
		}, wantState: StateUnsupported, wantReason: ReasonMatchAll},
		{name: "no policies", mutate: func(p *kyvernov2.PolicyException) { p.Spec.Exceptions = nil },
			wantState: StateUnsupported, wantReason: ReasonNoPolicies},
		{name: "subjects", mutate: func(p *kyvernov2.PolicyException) {
			p.Spec.Match.Any[0].Subjects = []rbacv1.Subject{{Kind: "User", Name: "alice"}}
		}, wantState: StateUnsupported, wantReason: ReasonSubjects},
		{name: "roles", mutate: func(p *kyvernov2.PolicyException) { p.Spec.Match.Any[0].Roles = []string{"ns:role"} },
			wantState: StateUnsupported, wantReason: ReasonRoles},
		{name: "cluster roles", mutate: func(p *kyvernov2.PolicyException) { p.Spec.Match.Any[0].ClusterRoles = []string{"admin"} },
			wantState: StateUnsupported, wantReason: ReasonClusterRoles},
		{name: "selector", mutate: func(p *kyvernov2.PolicyException) { p.Spec.Match.Any[0].Selector = &metav1.LabelSelector{} },
			wantState: StateUnsupported, wantReason: ReasonSelector},
		{name: "namespace selector", mutate: func(p *kyvernov2.PolicyException) {
			p.Spec.Match.Any[0].NamespaceSelector = &metav1.LabelSelector{}
		}, wantState: StateUnsupported, wantReason: ReasonNamespaceSelector},
		{name: "annotations", mutate: func(p *kyvernov2.PolicyException) { p.Spec.Match.Any[0].Annotations = map[string]string{"a": "b"} },
			wantState: StateUnsupported, wantReason: ReasonAnnotations},
		{name: "operations", mutate: func(p *kyvernov2.PolicyException) {
			p.Spec.Match.Any[0].Operations = []kyvernov1.AdmissionOperation{kyvernov1.Create}
		}, wantState: StateUnsupported, wantReason: ReasonOperations},
		{name: "no kinds", mutate: func(p *kyvernov2.PolicyException) { p.Spec.Match.Any[0].Kinds = nil },
			wantState: StateUnsupported, wantReason: ReasonNoKinds},
		{name: "namespaced policy", mutate: func(p *kyvernov2.PolicyException) { p.Spec.Exceptions[0].PolicyName = "team/my-policy" },
			wantState: StateUnsupported, wantReason: ReasonNamespacedPolicy},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			lookup := tc.lookup
			if lookup == nil {
				lookup = clusterPolicies
			}
			got, err := Translate(source(tc.mutate), lookup)
			if err != nil {
				t.Fatal(err)
			}
			if got.State != tc.wantState || got.Reason != tc.wantReason {
				t.Fatalf("got state %q reason %q, want %q %q", got.State, got.Reason, tc.wantState, tc.wantReason)
			}
		})
	}
}

func TestTranslateKindFormats(t *testing.T) {
	cases := []struct {
		kind     string
		wantKind string // "" means unsupported with reason kind_format
	}{
		{kind: "Pod", wantKind: "Pod"},
		{kind: "v1/Pod", wantKind: "Pod"},
		{kind: "apps/v1/Deployment", wantKind: "Deployment"},
		{kind: "autoscaling/v2beta1/HorizontalPodAutoscaler", wantKind: "HorizontalPodAutoscaler"},
		{kind: "*/Deployment", wantKind: "Deployment"},
		{kind: "apps/*/Deployment", wantKind: "Deployment"},
		{kind: "Pod/exec"},
		{kind: "Pod.exec"},
		{kind: "v1/Pod/exec"},
		{kind: "apps/v1/Deployment/scale"},
		{kind: "*/exec"},
		{kind: "*"},
		{kind: "*/*"},
		{kind: "Dae?onSet"},
		{kind: "apps/v1/*Set"},
		{kind: "a/b/c/d/e"},
	}
	for _, tc := range cases {
		t.Run(tc.kind, func(t *testing.T) {
			got, err := Translate(source(func(p *kyvernov2.PolicyException) { p.Spec.Match.Any[0].Kinds = []string{tc.kind} }), clusterPolicies)
			if err != nil {
				t.Fatal(err)
			}
			if tc.wantKind == "" {
				if got.State != StateUnsupported || got.Reason != ReasonKindFormat {
					t.Fatalf("got state %q reason %q, want unsupported kind_format", got.State, got.Reason)
				}
				return
			}
			if got.State != "" || len(got.Spec.Targets) != 1 || got.Spec.Targets[0].Kind != tc.wantKind {
				t.Fatalf("got state %q targets %+v, want exact with kind %q", got.State, got.Spec.Targets, tc.wantKind)
			}
		})
	}
}

func TestTranslateLookupError(t *testing.T) {
	boom := errors.New("boom")
	_, err := Translate(source(nil), func(string) ([]string, bool, error) { return nil, false, boom })
	if !errors.Is(err, boom) {
		t.Fatalf("got %v, want %v", err, boom)
	}
}

func TestTranslateNilListsBecomeEmpty(t *testing.T) {
	// The gspolex CRD requires names and namespaces; null would be rejected.
	got, err := Translate(source(func(p *kyvernov2.PolicyException) {
		p.Spec.Match.Any = kyvernov1.ResourceFilters{{ResourceDescription: kyvernov1.ResourceDescription{Kinds: []string{"Pod"}}}}
	}), clusterPolicies)
	if err != nil {
		t.Fatal(err)
	}
	target := got.Spec.Targets[0]
	if target.Names == nil || target.Namespaces == nil {
		t.Fatalf("got nil lists in %+v", target)
	}
}
