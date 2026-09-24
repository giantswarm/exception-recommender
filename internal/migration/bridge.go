// Package migration translates legacy kyverno.io PolicyExceptions into Giant Swarm PolicyExceptions
// ("bridges") that kyverno-policy-operator turns into policies.kyverno.io PolicyExceptions.
package migration

import (
	"strings"

	policyAPI "github.com/giantswarm/policy-api/api/v1alpha1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	ComponentName          = "exception-recommender"
	KPOComponentName       = "kyverno-policy-operator"
	ManagedByLabel         = "app.kubernetes.io/managed-by"
	AnnotationMigratedFrom = "policy.giantswarm.io/migrated-from"
	BridgeSuffix           = "-migrated"
	// MaxNameLength is the longest object name the API server accepts (DNS subdomain).
	MaxNameLength = 253
	// LegacyCRDName is the CustomResourceDefinition of the sources.
	LegacyCRDName = "policyexceptions.kyverno.io"
)

// Migration states, the values of the "state" metric label.
const (
	StatePending     = "pending"
	StateMigrated    = "migrated"
	StateLossy       = "lossy"
	StateUnsupported = "unsupported"
	StateCollision   = "collision"
)

// States lists every state, so gauges can report zero for the ones not seen.
var States = []string{StatePending, StateMigrated, StateLossy, StateUnsupported, StateCollision}

// BridgeName is the name of the Giant Swarm PolicyException that bridges src.
func BridgeName(src client.Object) string {
	return src.GetName() + BridgeSuffix
}

// SourceKey is the value of the migrated-from annotation for src.
func SourceKey(src client.Object) string {
	return src.GetNamespace() + "/" + src.GetName()
}

// ParseSourceKey reads a migrated-from annotation value back into a namespaced name.
func ParseSourceKey(value string) (types.NamespacedName, bool) {
	namespace, name, ok := strings.Cut(value, "/")
	if !ok || namespace == "" || name == "" {
		return types.NamespacedName{}, false
	}
	return types.NamespacedName{Namespace: namespace, Name: name}, true
}

// IsManagedByKPO reports whether kyverno-policy-operator generated obj. Those are never bridged.
func IsManagedByKPO(obj client.Object) bool {
	return obj.GetLabels()[ManagedByLabel] == KPOComponentName
}

// IsBridgeFor reports whether bridge was written by exception-recommender for the source with this key.
// Only such objects may be updated or deleted.
func IsBridgeFor(bridge *policyAPI.PolicyException, sourceKey string) bool {
	return bridge.Labels[ManagedByLabel] == ComponentName && bridge.Annotations[AnnotationMigratedFrom] == sourceKey
}
