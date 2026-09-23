package migration

import (
	"slices"
	"strings"

	policyAPI "github.com/giantswarm/policy-api/api/v1alpha1"
	kyvernov1 "github.com/kyverno/kyverno/api/kyverno/v1"
	kyvernov2 "github.com/kyverno/kyverno/api/kyverno/v2"
	"github.com/kyverno/kyverno/ext/wildcard"
	kubeutils "github.com/kyverno/kyverno/pkg/utils/kube"
)

// Reasons, the values of the "reason" metric label.
const (
	ReasonNameTooLong       = "name_too_long"
	ReasonConditions        = "conditions"
	ReasonPodSecurity       = "pod_security"
	ReasonNoMatch           = "no_match"
	ReasonMatchAll          = "match_all"
	ReasonSubjects          = "subjects"
	ReasonRoles             = "roles"
	ReasonClusterRoles      = "cluster_roles"
	ReasonSelector          = "selector"
	ReasonNamespaceSelector = "namespace_selector"
	ReasonAnnotations       = "annotations"
	ReasonOperations        = "operations"
	ReasonNoKinds           = "no_kinds"
	ReasonKindFormat        = "kind_format"
	ReasonNoPolicies        = "no_policies"
	ReasonNamespacedPolicy  = "namespaced_policy"
	ReasonRuleNames         = "rule_names"
	ReasonPolicyNotFound    = "policy_not_found"
	ReasonBridgeMissing     = "bridge_missing"
	ReasonNameTaken         = "name_taken"
)

// RuleLookup returns the rules that ruleNames must cover for the policy called name: the rules of the
// ClusterPolicy, including autogen rules, or none when only a CEL policy (ValidatingPolicy,
// MutatingPolicy or ImageValidatingPolicy) has that name, because CEL policies have no rules.
// found is false when neither exists.
type RuleLookup func(name string) (rules []string, found bool, err error)

// Translation is the result of translating one legacy PolicyException.
type Translation struct {
	// State is empty when the translation is exact. Otherwise it is StateUnsupported, StateLossy or
	// StatePending, and Reason says why.
	State  string
	Reason string
	// Spec is the bridge spec. It is only complete when State is empty or StatePending.
	Spec policyAPI.PolicyExceptionSpec
}

// Translate turns a legacy PolicyException into a Giant Swarm PolicyException spec. The result is
// exact only if the bridge exempts the same requests as the source. Anything that cannot be
// expressed is unsupported. Exempting whole policies where the source names only some rules is lossy.
func Translate(src *kyvernov2.PolicyException, lookup RuleLookup) (Translation, error) {
	if reason := unsupportedReason(src); reason != "" {
		return Translation{State: StateUnsupported, Reason: reason}, nil
	}

	filters := append(append(kyvernov1.ResourceFilters{}, src.Spec.Match.All...), src.Spec.Match.Any...)
	spec := policyAPI.PolicyExceptionSpec{Policies: []string{}, Targets: []policyAPI.Target{}}
	for _, filter := range filters {
		names := append([]string{}, filter.Names...)
		if filter.Name != "" {
			names = append(names, filter.Name)
		}
		namespaces := append([]string{}, filter.Namespaces...)
		for _, kind := range filter.Kinds {
			plain, _ := targetKind(kind)
			spec.Targets = append(spec.Targets, policyAPI.Target{Kind: plain, Names: names, Namespaces: namespaces})
		}
	}
	for _, exception := range src.Spec.Exceptions {
		if !slices.Contains(spec.Policies, exception.PolicyName) {
			spec.Policies = append(spec.Policies, exception.PolicyName)
		}
	}

	pending := false
	for _, exception := range src.Spec.Exceptions {
		if slices.Contains(exception.RuleNames, "*") {
			continue
		}
		rules, found, err := lookup(exception.PolicyName)
		if err != nil {
			return Translation{}, err
		}
		if !found {
			pending = true
			continue
		}
		if !coversRules(exception.RuleNames, rules, hasKind(spec.Targets, "CronJob")) {
			return Translation{State: StateLossy, Reason: ReasonRuleNames, Spec: spec}, nil
		}
	}
	if pending {
		return Translation{State: StatePending, Reason: ReasonPolicyNotFound, Spec: spec}, nil
	}
	return Translation{Spec: spec}, nil
}

// unsupportedReason returns why src cannot be expressed as a Giant Swarm PolicyException, or "".
func unsupportedReason(src *kyvernov2.PolicyException) string {
	spec := src.Spec
	switch {
	case len(BridgeName(src)) > MaxNameLength:
		return ReasonNameTooLong
	case spec.Conditions != nil:
		return ReasonConditions
	case len(spec.PodSecurity) > 0:
		return ReasonPodSecurity
	case len(spec.Match.Any) == 0 && len(spec.Match.All) == 0:
		return ReasonNoMatch
	case len(spec.Match.All) > 1:
		// A gspolex target list is an "any"; an "all" of several filters is an intersection.
		return ReasonMatchAll
	case len(spec.Exceptions) == 0:
		return ReasonNoPolicies
	}
	for _, filter := range append(append(kyvernov1.ResourceFilters{}, spec.Match.All...), spec.Match.Any...) {
		if reason := unsupportedFilterReason(filter); reason != "" {
			return reason
		}
	}
	for _, exception := range spec.Exceptions {
		if strings.Contains(exception.PolicyName, "/") {
			// "<namespace>/<name>" references a namespaced Policy; gspolexes only name cluster policies.
			return ReasonNamespacedPolicy
		}
	}
	return ""
}

func unsupportedFilterReason(filter kyvernov1.ResourceFilter) string {
	switch {
	case len(filter.Subjects) > 0:
		return ReasonSubjects
	case len(filter.Roles) > 0:
		return ReasonRoles
	case len(filter.ClusterRoles) > 0:
		return ReasonClusterRoles
	case filter.Selector != nil:
		return ReasonSelector
	case filter.NamespaceSelector != nil:
		return ReasonNamespaceSelector
	case len(filter.Annotations) > 0:
		return ReasonAnnotations
	case len(filter.Operations) > 0:
		return ReasonOperations
	case len(filter.Kinds) == 0:
		return ReasonNoKinds
	}
	for _, kind := range filter.Kinds {
		if _, ok := targetKind(kind); !ok {
			return ReasonKindFormat
		}
	}
	return ""
}

// targetKind returns the plain kind of a Kyverno kind selector, because KPO only compares
// object.kind. It reads the selector with Kyverno's own parser, where a version is "*" or
// "v<N>[alpha|beta<N>]":
//   - "Kind", "version/Kind" and "group/version/Kind" become "Kind"; the group and version are dropped.
//   - A subresource is rejected: "Kind/sub" (two parts, the first not a version), "version/Kind/sub",
//     "group/version/Kind/sub" and "Kind.sub".
//   - A wildcard ("*" or "?") in the kind is rejected, and so is a selector of five or more parts.
func targetKind(kind string) (string, bool) {
	_, _, plain, subresource := kubeutils.ParseKindSelector(kind)
	if plain == "" || subresource != "" || strings.ContainsAny(plain, "*?") {
		return "", false
	}
	return plain, true
}

// coversRules reports whether ruleNames exempt every rule. "autogen-cronjob-" rules only apply to
// CronJobs, so they may be left out unless a target is a CronJob.
func coversRules(ruleNames, rules []string, cronJobTarget bool) bool {
	for _, rule := range rules {
		if !cronJobTarget && strings.HasPrefix(rule, "autogen-cronjob-") {
			continue
		}
		if !slices.ContainsFunc(ruleNames, func(pattern string) bool { return wildcard.Match(pattern, rule) }) {
			return false
		}
	}
	return true
}

func hasKind(targets []policyAPI.Target, kind string) bool {
	return slices.ContainsFunc(targets, func(t policyAPI.Target) bool { return t.Kind == kind })
}
