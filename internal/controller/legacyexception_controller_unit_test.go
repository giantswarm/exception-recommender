package controller

import (
	"context"
	"errors"
	"testing"

	policyAPI "github.com/giantswarm/policy-api/api/v1alpha1"
	policiesv1 "github.com/kyverno/api/api/policies.kyverno.io/v1"
	kyvernov1 "github.com/kyverno/kyverno/api/kyverno/v1"
	kyvernov2 "github.com/kyverno/kyverno/api/kyverno/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/giantswarm/exception-recommender/internal/migration"
	"github.com/giantswarm/exception-recommender/internal/testsupport"
)

// newReconciler uses cached as the manager's cached client and api as the uncached API reader.
func newReconciler(cached, api client.Client) *LegacyExceptionReconciler {
	return &LegacyExceptionReconciler{Client: cached, APIReader: api, BridgeNamespace: testBridgeNamespace}
}

// reconcileSource reconciles the sources at keys in order, or sourceKey when none are given.
func reconcileSource(t *testing.T, r *LegacyExceptionReconciler, keys ...types.NamespacedName) error {
	t.Helper()
	if len(keys) == 0 {
		keys = []types.NamespacedName{sourceKey}
	}
	for _, key := range keys {
		if _, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: key}); err != nil {
			return err
		}
	}
	return nil
}

func TestReconcileWritesBridge(t *testing.T) {
	// arrange
	c := newClient(t, nonrootPolicy(), exactSource())

	// act
	err := reconcileSource(t, newReconciler(c, c))

	// assert
	require.NoError(t, err)
	bridge := requireBridge(t, c)
	assert.Equal(t, migration.ComponentName, bridge.Labels[migration.ManagedByLabel])
	assert.Equal(t, migration.SourceKey(exactSource()), bridge.Annotations[migration.AnnotationMigratedFrom])
	assert.Equal(t, []string{testPolicyName}, bridge.Spec.Policies)
	assert.Len(t, bridge.Spec.Targets, 2)
	assert.Empty(t, bridge.OwnerReferences)
}

func TestReconcileUpdatesOwnBridge(t *testing.T) {
	// arrange
	c := newClient(t, nonrootPolicy(), exactSource(), bridgeOf(exactSource()))

	// act
	err := reconcileSource(t, newReconciler(c, c))

	// assert
	require.NoError(t, err)
	bridge := requireBridge(t, c)
	assert.Equal(t, []string{testPolicyName}, bridge.Spec.Policies, "bridge not updated")
	assert.Len(t, bridge.Spec.Targets, 2, "bridge not updated")
}

func TestReconcileNeverTouchesHandWrittenBridge(t *testing.T) {
	for name, objects := range map[string][]client.Object{
		"source exists": {nonrootPolicy(), exactSource(), handWrittenBridgeFor(exactSource()), legacyCRD()},
		"source gone":   {handWrittenBridgeFor(exactSource()), legacyCRD()},
	} {
		t.Run(name, func(t *testing.T) {
			// arrange
			c := newClient(t, objects...)
			before := requireBridge(t, c)

			// act
			err := reconcileSource(t, newReconciler(c, c))

			// assert
			require.NoError(t, err)
			assertBridgeUnchanged(t, c, before)
		})
	}
}

func TestReconcileSkipsKPOManagedSource(t *testing.T) {
	// arrange
	c := newClient(t, nonrootPolicy(), kpoSource())

	// act
	err := reconcileSource(t, newReconciler(c, c))

	// assert
	require.NoError(t, err)
	assertNoBridge(t, c, "bridged a kyverno-policy-operator exception")
}

func TestReconcileKeepsBridgeWhenSourceDrifts(t *testing.T) {
	for name, src := range map[string]*kyvernov2.PolicyException{
		"lossy":       lossySource(),
		"unsupported": unsupportedSource(),
	} {
		t.Run(name, func(t *testing.T) {
			// arrange
			c := newClient(t, nonrootPolicy(), src, bridgeOf(src), legacyCRD())
			before := requireBridge(t, c)
			removed := testsupport.CounterDelta(BridgesRemoved)

			// act
			err := reconcileSource(t, newReconciler(c, c))

			// assert
			require.NoError(t, err)
			assertBridgeUnchanged(t, c, before)
			assert.Zero(t, removed(), "bridges_removed_total grew")
		})
	}
}

// Exact source -> bridge; source edited to be lossy -> bridge kept unchanged, state lossy;
// source deleted -> bridge removed.
func TestReconcileBridgeLifecycle(t *testing.T) {
	ctx := context.Background()
	src := exactSource()
	c := newClient(t, nonrootPolicy(), src, legacyCRD())
	r := newReconciler(c, c)

	// exact source: bridge written
	require.NoError(t, reconcileSource(t, r))
	written := requireBridge(t, c)

	// source turns lossy: bridge kept unchanged
	require.NoError(t, c.Get(ctx, sourceKey, src))
	src.Spec.Exceptions = lossySource().Spec.Exceptions
	require.NoError(t, c.Update(ctx, src))

	require.NoError(t, reconcileSource(t, r))

	assertBridgeUnchanged(t, c, written)
	status, err := migration.Evaluate(ctx, c, testBridgeNamespace, src)
	require.NoError(t, err)
	assert.Equal(t, migration.StateLossy, status.State)
	assert.Equal(t, migration.ReasonRuleNames, status.Reason)
	assert.True(t, status.OwnsBridge(src), "bridge no longer owned by the source")

	// source deleted: bridge removed
	require.NoError(t, c.Delete(ctx, src))

	require.NoError(t, reconcileSource(t, r))

	assertNoBridge(t, c, "bridge of a deleted source kept")
}

// The ClusterPolicy was replaced by a ValidatingPolicy of the same name: ruleNames are not checked,
// so even a source that was lossy against the ClusterPolicy is bridged.
func TestReconcileBridgesThroughCELPolicy(t *testing.T) {
	// arrange
	c := newClient(t, &policiesv1.ValidatingPolicy{ObjectMeta: metav1.ObjectMeta{Name: testPolicyName}}, lossySource())

	// act
	err := reconcileSource(t, newReconciler(c, c))

	// assert
	require.NoError(t, err)
	assert.Equal(t, []string{testPolicyName}, requireBridge(t, c).Spec.Policies)
}

func TestReconcileWritesNothingWithoutAnyPolicy(t *testing.T) {
	// arrange
	src := exactSource()
	c := newClient(t, src)

	// act
	err := reconcileSource(t, newReconciler(c, c))

	// assert
	require.NoError(t, err)
	assertNoBridge(t, c, "bridged a source whose policy does not exist")
	status, err := migration.Evaluate(context.Background(), c, testBridgeNamespace, src)
	require.NoError(t, err)
	assert.Equal(t, migration.StatePending, status.State)
	assert.Equal(t, migration.ReasonPolicyNotFound, status.Reason)
}

// Two sources with the same name share a bridge name. The one whose "namespace/name" sorts lowest
// gets it, unless the other already owns the bridge.
func TestReconcileSameNameTieBreak(t *testing.T) {
	higher := exactSource() // giantswarm/cilium
	lower := inNamespace(exactSource(), "default")
	higherKey, lowerKey := client.ObjectKeyFromObject(higher), client.ObjectKeyFromObject(lower)

	for name, order := range map[string][]types.NamespacedName{
		"higher reconciled first": {higherKey, lowerKey},
		"lower reconciled first":  {lowerKey, higherKey},
	} {
		t.Run("no bridge yet, "+name, func(t *testing.T) {
			// arrange
			c := newClient(t, nonrootPolicy(), higher.DeepCopy(), lower.DeepCopy())

			// act
			err := reconcileSource(t, newReconciler(c, c), order...)

			// assert
			require.NoError(t, err)
			assert.Equal(t, migration.SourceKey(lower), requireBridge(t, c).Annotations[migration.AnnotationMigratedFrom],
				"bridge not owned by the lowest-sorting source")
		})
		t.Run("existing bridge of the higher source, "+name, func(t *testing.T) {
			// arrange
			c := newClient(t, nonrootPolicy(), higher.DeepCopy(), lower.DeepCopy(), bridgeOf(higher))

			// act
			err := reconcileSource(t, newReconciler(c, c), order...)

			// assert
			require.NoError(t, err)
			assert.Equal(t, migration.SourceKey(higher), requireBridge(t, c).Annotations[migration.AnnotationMigratedFrom],
				"bridge ownership moved")
		})
	}
}

func TestReconcileRemovesBridgeWhenSourceDeleted(t *testing.T) {
	// arrange
	c := newClient(t, bridgeOf(exactSource()), legacyCRD())
	removed := testsupport.CounterDelta(BridgesRemoved)

	// act
	err := reconcileSource(t, newReconciler(c, c))

	// assert
	require.NoError(t, err)
	assertNoBridge(t, c, "bridge of a deleted source kept")
	assert.Equal(t, 1.0, removed(), "bridges_removed_total growth")
}

// The cached client says the source is gone; the bridge is only deleted once the API server
// confirms it and the legacy CRD is healthy.
func TestReconcileKeepsBridgeWhenDeletionIsNotConfirmed(t *testing.T) {
	now := metav1.Now()
	terminating := legacyCRD()
	terminating.DeletionTimestamp, terminating.Finalizers = &now, []string{"customresourcecleanup.apiextensions.k8s.io"}
	notServed := legacyCRD()
	notServed.Spec.Versions[0].Served = false

	cases := map[string]struct {
		api     func(t *testing.T) client.Client
		wantErr error
	}{
		"source still on the API server": {api: func(t *testing.T) client.Client { return newClient(t, legacyCRD(), exactSource()) }},
		"legacy CRD being deleted":       {api: func(t *testing.T) client.Client { return newClient(t, terminating) }},
		"legacy CRD not found":           {api: func(t *testing.T) client.Client { return newClient(t) }},
		"legacy CRD does not serve v2":   {api: func(t *testing.T) client.Client { return newClient(t, notServed) }},
		"API server unreachable": {api: func(t *testing.T) client.Client {
			return newClientWith(t, testsupport.FailAllGets(testsupport.ErrBoom))
		}, wantErr: testsupport.ErrBoom},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			// arrange
			cached := newClient(t, bridgeOf(exactSource()))
			before := requireBridge(t, cached)

			// act
			err := reconcileSource(t, newReconciler(cached, tc.api(t)))

			// assert
			if tc.wantErr != nil {
				assert.ErrorIs(t, err, tc.wantErr)
			} else {
				assert.NoError(t, err)
			}
			assertBridgeUnchanged(t, cached, before)
		})
	}
}

// The cache still holds the checked bridge while the API server has a new object of that name.
func TestReconcileKeepsBridgeRecreatedBeforeDelete(t *testing.T) {
	// arrange
	recreated := bridgeOf(exactSource())
	recreated.UID = "recreated"
	funcs := interceptor.Funcs{
		// The cache serves the bridge as it was when checked.
		Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
			if err := c.Get(ctx, key, obj, opts...); err != nil {
				return err
			}
			if bridge, ok := obj.(*policyAPI.PolicyException); ok {
				bridge.UID = "checked"
			}
			return nil
		},
		// The fake client ignores UID preconditions; reject a mismatch like the API server does.
		Delete: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.DeleteOption) error {
			var options client.DeleteOptions
			options.ApplyOptions(opts)
			if options.Preconditions != nil && options.Preconditions.UID != nil && *options.Preconditions.UID != recreated.UID {
				return apierrors.NewConflict(policyAPI.GroupVersion.WithResource("policyexceptions").GroupResource(),
					obj.GetName(), errors.New("UID precondition failed"))
			}
			return c.Delete(ctx, obj, opts...)
		},
	}
	cached := newClientWith(t, funcs, recreated)
	api := newClient(t, legacyCRD())
	removed := testsupport.CounterDelta(BridgesRemoved)

	// act
	err := reconcileSource(t, newReconciler(cached, api))

	// assert
	assert.True(t, apierrors.IsConflict(err), "got error %v, want a conflict", err)
	requireBridge(t, cached)
	assert.Zero(t, removed(), "bridges_removed_total grew")
}

// Neither a ClusterPolicy nor a CEL policy of that name exists any more: the bridge stays as written.
func TestReconcileKeepsBridgeWhenPolicyGone(t *testing.T) {
	// arrange
	c := newClient(t, exactSource(), bridgeOf(exactSource()))
	before := requireBridge(t, c)

	// act
	err := reconcileSource(t, newReconciler(c, c))

	// assert
	require.NoError(t, err)
	assertBridgeUnchanged(t, c, before)
}

// Evaluate saw no bridge; by the time the bridge is written, someone else created that name.
func TestReconcileNeverTakesOverObjectCreatedAfterEvaluate(t *testing.T) {
	// arrange
	hideNextBridgeGet := false
	c := newClientWith(t, interceptor.Funcs{Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
		if _, ok := obj.(*policyAPI.PolicyException); ok && hideNextBridgeGet {
			hideNextBridgeGet = false
			return apierrors.NewNotFound(policyAPI.GroupVersion.WithResource("policyexceptions").GroupResource(), key.Name)
		}
		return c.Get(ctx, key, obj, opts...)
	}}, nonrootPolicy(), exactSource(), handWrittenBridgeFor(exactSource()))
	before := requireBridge(t, c)
	hideNextBridgeGet = true
	applyFailed := testsupport.CounterDelta(TranslationErrors.WithLabelValues(ReasonApplyFailed))

	// act
	err := reconcileSource(t, newReconciler(c, c))

	// assert
	require.NoError(t, err, "a name collision is not an error")
	assertBridgeUnchanged(t, c, before)
	assert.Zero(t, applyFailed(), "apply_failed grew")
}

func TestReconcileWriteFailure(t *testing.T) {
	// arrange
	c := newClientWith(t, testsupport.FailCreate(testsupport.ErrBoom), nonrootPolicy(), exactSource())
	applyFailed := testsupport.CounterDelta(TranslationErrors.WithLabelValues(ReasonApplyFailed))

	// act
	err := reconcileSource(t, newReconciler(c, c))

	// assert
	assert.ErrorIs(t, err, testsupport.ErrBoom)
	assert.Equal(t, 1.0, applyFailed(), "apply_failed growth")
}

func TestReconcileSourceGetFails(t *testing.T) {
	// arrange
	cached := newClientWith(t, testsupport.FailGet[*kyvernov2.PolicyException](testsupport.ErrBoom),
		bridgeOf(exactSource()), legacyCRD())
	// The API reader has no source either: only the failed cache read may stop the delete.
	api := newClient(t, legacyCRD())

	// act
	err := reconcileSource(t, newReconciler(cached, api))

	// assert
	assert.ErrorIs(t, err, testsupport.ErrBoom)
	requireBridge(t, cached)
}

func TestReconcileEvaluateFails(t *testing.T) {
	// arrange
	c := newClientWith(t, testsupport.FailGet[*kyvernov1.ClusterPolicy](testsupport.ErrBoom), nonrootPolicy(), exactSource())
	lookupFailed := testsupport.CounterDelta(TranslationErrors.WithLabelValues(ReasonLookupFailed))

	// act
	err := reconcileSource(t, newReconciler(c, c))

	// assert
	assert.ErrorIs(t, err, testsupport.ErrBoom)
	assertNoBridge(t, c, "bridge written although the policy lookup failed")
	assert.Equal(t, 1.0, lookupFailed(), "lookup_failed growth")
}

func TestReconcileBridgeGetFailsWhenSourceGone(t *testing.T) {
	// arrange
	c := newClientWith(t, testsupport.FailGet[*policyAPI.PolicyException](testsupport.ErrBoom), legacyCRD())

	// act
	err := reconcileSource(t, newReconciler(c, c))

	// assert
	assert.ErrorIs(t, err, testsupport.ErrBoom)
}

func TestReconcileDeleteOutcomes(t *testing.T) {
	cases := map[string]struct {
		deleteErr       error
		wantErr         error
		wantDeleteFails float64
	}{
		"already deleted by someone else": {
			deleteErr: apierrors.NewNotFound(policyAPI.GroupVersion.WithResource("policyexceptions").GroupResource(), bridgeKey.Name),
		},
		"delete fails": {deleteErr: testsupport.ErrBoom, wantErr: testsupport.ErrBoom, wantDeleteFails: 1},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			// arrange
			c := newClientWith(t, testsupport.FailDelete(tc.deleteErr), bridgeOf(exactSource()), legacyCRD())
			removed := testsupport.CounterDelta(BridgesRemoved)
			deleteFailed := testsupport.CounterDelta(TranslationErrors.WithLabelValues(ReasonDeleteFailed))

			// act
			err := reconcileSource(t, newReconciler(c, c))

			// assert
			if tc.wantErr != nil {
				assert.ErrorIs(t, err, tc.wantErr)
			} else {
				assert.NoError(t, err)
			}
			assert.Zero(t, removed(), "bridges_removed_total counts only deletes this controller made")
			assert.Equal(t, tc.wantDeleteFails, deleteFailed(), "delete_failed growth")
		})
	}
}

func TestBridgeToSource(t *testing.T) {
	kpoLabelled := bridgeOf(exactSource())
	kpoLabelled.Labels[migration.ManagedByLabel] = migration.KPOComponentName
	noAnnotation := bridgeOf(exactSource())
	noAnnotation.Annotations = nil
	malformedAnnotation := bridgeOf(exactSource())
	malformedAnnotation.Annotations[migration.AnnotationMigratedFrom] = sourceKey.Name

	cases := map[string]struct {
		bridge *policyAPI.PolicyException
		want   []reconcile.Request
	}{
		"own bridge":                         {bridge: bridgeOf(exactSource()), want: []reconcile.Request{{NamespacedName: sourceKey}}},
		"no managed-by label":                {bridge: handWrittenBridgeFor(exactSource())},
		"managed by kyverno-policy-operator": {bridge: kpoLabelled},
		"no migrated-from annotation":        {bridge: noAnnotation},
		"malformed migrated-from annotation": {bridge: malformedAnnotation},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got := bridgeToSource(context.Background(), tc.bridge)

			assert.Equal(t, tc.want, got)
		})
	}
}
