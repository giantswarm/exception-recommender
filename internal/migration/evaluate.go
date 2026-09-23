package migration

import (
	"context"
	"fmt"

	policyAPI "github.com/giantswarm/policy-api/api/v1alpha1"
	policiesv1 "github.com/kyverno/api/api/policies.kyverno.io/v1"
	kyvernov1 "github.com/kyverno/kyverno/api/kyverno/v1"
	kyvernov2 "github.com/kyverno/kyverno/api/kyverno/v2"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// Status is the migration state of one legacy PolicyException. The reconciler acts on it and the
// metrics collector reports it, so both always agree.
type Status struct {
	State  string
	Reason string
	// Write is true when the bridge must be created or updated with Spec.
	Write bool
	Spec  policyAPI.PolicyExceptionSpec
	// Bridge is the existing object named BridgeName(src) in the bridge namespace, or nil.
	Bridge *policyAPI.PolicyException
}

// OwnsBridge reports whether Bridge exists and was written for src.
func (s Status) OwnsBridge(src client.Object) bool {
	return s.Bridge != nil && IsBridgeFor(s.Bridge, SourceKey(src))
}

// Evaluate decides the migration state of src.
func Evaluate(ctx context.Context, c client.Reader, bridgeNamespace string, src *kyvernov2.PolicyException) (Status, error) {
	logger := log.FromContext(ctx).WithValues("source", SourceKey(src))

	lookup := PolicyRules(ctx, c)
	translation, err := Translate(src, lookup)
	if err != nil {
		return Status{}, err
	}
	status := Status{State: translation.State, Reason: translation.Reason, Spec: translation.Spec}

	// A name that is too long cannot exist; every other state needs the bridge, even unsupported and
	// lossy ones, whose earlier bridge is kept.
	if translation.Reason != ReasonNameTooLong {
		var bridge policyAPI.PolicyException
		err := c.Get(ctx, types.NamespacedName{Namespace: bridgeNamespace, Name: BridgeName(src)}, &bridge)
		switch {
		case err == nil:
			status.Bridge = &bridge
		case apierrors.IsNotFound(err):
			logger.V(1).Info("no bridge yet", "bridge", BridgeName(src))
		default:
			logger.Error(err, "unable to get bridge", "bridge", BridgeName(src))
			return Status{}, err
		}
	}

	switch {
	case translation.State == StateUnsupported || translation.State == StateLossy:
		// Never written. A bridge written while the source was exact is kept as it was, and the
		// state stays lossy or unsupported so the drift shows in the metrics.
	case status.Bridge != nil && !status.OwnsBridge(src):
		status.State, status.Reason = StateCollision, ReasonNameTaken
	case translation.State == StatePending && status.OwnsBridge(src):
		// Neither a ClusterPolicy nor a CEL policy of that name exists any more. The bridge was
		// exact when written, so it stays as it is.
		status.State, status.Reason = StateMigrated, ""
	case translation.State == StatePending:
		// Nothing to check ruleNames against, and no policy to exempt; write nothing.
	case status.Bridge != nil:
		status.State, status.Write = StateMigrated, true
	default:
		claimed, err := claimedByLowerSource(ctx, c, src, lookup)
		if err != nil {
			return Status{}, err
		}
		if claimed {
			status.State, status.Reason = StateCollision, ReasonNameTaken
		} else {
			status.State, status.Reason, status.Write = StatePending, ReasonBridgeMissing, true
		}
	}
	return status, nil
}

// claimedByLowerSource reports whether another exact source with the same name, whose
// "namespace/name" sorts lower, claims the bridge name. It only decides while no bridge exists; an
// existing bridge keeps its owner, so ownership never moves.
func claimedByLowerSource(ctx context.Context, c client.Reader, src *kyvernov2.PolicyException, lookup RuleLookup) (bool, error) {
	logger := log.FromContext(ctx)
	var sources kyvernov2.PolicyExceptionList
	if err := c.List(ctx, &sources); err != nil {
		logger.Error(err, "unable to list legacy PolicyExceptions")
		return false, err
	}
	for i := range sources.Items {
		other := &sources.Items[i]
		if other.Name != src.Name || SourceKey(other) >= SourceKey(src) || IsManagedByKPO(other) {
			continue
		}
		translation, err := Translate(other, lookup)
		if err != nil {
			return false, err
		}
		if translation.State == "" {
			logger.V(1).Info("bridge name claimed by a lower-sorting source", "other", SourceKey(other))
			return true, nil
		}
	}
	return false, nil
}

// celPolicies returns empty objects of the cluster-scoped CEL policy kinds a Giant Swarm
// PolicyException can name.
func celPolicies() []client.Object {
	return []client.Object{&policiesv1.ValidatingPolicy{}, &policiesv1.MutatingPolicy{}, &policiesv1.ImageValidatingPolicy{}}
}

// PolicyRules returns a RuleLookup that reads policies through c: the ClusterPolicy's rules, or no
// rules when only a CEL policy of that name exists.
func PolicyRules(ctx context.Context, c client.Reader) RuleLookup {
	logger := log.FromContext(ctx)
	return func(name string) ([]string, bool, error) {
		key := types.NamespacedName{Name: name}
		var policy kyvernov1.ClusterPolicy
		if err := c.Get(ctx, key, &policy); err == nil {
			var rules []string
			for _, rule := range policy.Spec.Rules {
				rules = append(rules, rule.Name)
			}
			for _, rule := range policy.Status.Autogen.Rules {
				rules = append(rules, rule.Name)
			}
			logger.V(1).Info("ClusterPolicy rules", "policy", name, "rules", rules)
			return rules, true, nil
		} else if !notFound(err) {
			logger.Error(err, "unable to get ClusterPolicy", "policy", name)
			return nil, false, err
		}

		for _, obj := range celPolicies() {
			if err := c.Get(ctx, key, obj); err == nil {
				logger.V(1).Info("no ClusterPolicy, found a CEL policy with no rules to check", "policy", name, "type", fmt.Sprintf("%T", obj))
				return nil, true, nil
			} else if !notFound(err) {
				logger.Error(err, "unable to get CEL policy", "policy", name, "type", fmt.Sprintf("%T", obj))
				return nil, false, err
			}
		}
		logger.V(1).Info("neither a ClusterPolicy nor a CEL policy found", "policy", name)
		return nil, false, nil
	}
}

// notFound treats a kind the API server does not serve like a missing object.
func notFound(err error) bool {
	return apierrors.IsNotFound(err) || meta.IsNoMatchError(err)
}
