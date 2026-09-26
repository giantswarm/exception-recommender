package controller

import (
	"context"
	"errors"
	"testing"

	policyAPI "github.com/giantswarm/policy-api/api/v1alpha1"
	policiesv1 "github.com/kyverno/api/api/policies.kyverno.io/v1"
	kyvernov1 "github.com/kyverno/kyverno/api/kyverno/v1"
	kyvernov2 "github.com/kyverno/kyverno/api/kyverno/v2"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/giantswarm/exception-recommender/internal/migration"
)

// newReconciler uses cached as the manager's cached client and api as the uncached API reader.
func newReconciler(cached, api client.Client) *LegacyExceptionReconciler {
	return &LegacyExceptionReconciler{Client: cached, APIReader: api, BridgeNamespace: testBridgeNamespace}
}

func reconcileSource(t *testing.T, r *LegacyExceptionReconciler) error {
	t.Helper()
	_, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: sourceKey})
	return err
}

func getBridge(t *testing.T, c client.Client) (*policyAPI.PolicyException, bool) {
	t.Helper()
	var bridge policyAPI.PolicyException
	err := c.Get(context.Background(), bridgeKey, &bridge)
	if apierrors.IsNotFound(err) {
		return nil, false
	}
	require.NoError(t, err)
	return &bridge, true
}

func TestReconcileWritesBridge(t *testing.T) {
	// arrange
	s := unitScheme(t)
	c := fake.NewClientBuilder().WithScheme(s).
		WithObjects(nonrootPolicy(), legacySource("run-as-non-root", "autogen-run-as-non-root")).Build()

	// act
	err := reconcileSource(t, newReconciler(c, c))

	// assert
	require.NoError(t, err)
	bridge, ok := getBridge(t, c)
	require.True(t, ok, "bridge not created")
	assert.Equal(t, migration.ComponentName, bridge.Labels[migration.ManagedByLabel])
	assert.Equal(t, "giantswarm/cilium", bridge.Annotations[migration.AnnotationMigratedFrom])
	assert.Len(t, bridge.Spec.Policies, 1)
	assert.Len(t, bridge.Spec.Targets, 2)
	assert.Empty(t, bridge.OwnerReferences)
}

func TestReconcileUpdatesOwnBridge(t *testing.T) {
	// arrange
	s := unitScheme(t)
	c := fake.NewClientBuilder().WithScheme(s).WithObjects(nonrootPolicy(),
		legacySource("*"), existingBridge(ownLabels, "giantswarm/cilium")).Build()

	// act
	err := reconcileSource(t, newReconciler(c, c))

	// assert
	require.NoError(t, err)
	bridge, ok := getBridge(t, c)
	require.True(t, ok)
	assert.Equal(t, []string{testPolicyName}, bridge.Spec.Policies, "bridge not updated")
}

func TestReconcileNeverTouchesUnlabelledGSPolex(t *testing.T) {
	s := unitScheme(t)
	for name, objects := range map[string][]client.Object{
		"source exists": {nonrootPolicy(), legacySource("*"), existingBridge(nil, "giantswarm/cilium"), legacyCRD()},
		"source gone":   {existingBridge(nil, "giantswarm/cilium"), legacyCRD()},
	} {
		t.Run(name, func(t *testing.T) {
			// arrange
			c := fake.NewClientBuilder().WithScheme(s).WithObjects(objects...).Build()

			// act
			err := reconcileSource(t, newReconciler(c, c))

			// assert
			require.NoError(t, err)
			bridge, ok := getBridge(t, c)
			require.True(t, ok, "unlabelled gspolex was deleted")
			assert.Equal(t, []string{stalePolicy}, bridge.Spec.Policies, "unlabelled gspolex was changed")
		})
	}
}

func TestReconcileSkipsKPOManagedSource(t *testing.T) {
	// arrange
	s := unitScheme(t)
	src := legacySource("*")
	src.Labels = map[string]string{migration.ManagedByLabel: migration.KPOComponentName}
	c := fake.NewClientBuilder().WithScheme(s).WithObjects(nonrootPolicy(), src).Build()

	// act
	err := reconcileSource(t, newReconciler(c, c))

	// assert
	require.NoError(t, err)
	_, ok := getBridge(t, c)
	assert.False(t, ok, "bridged a kyverno-policy-operator exception")
}

func TestReconcileKeepsBridgeWhenSourceDrifts(t *testing.T) {
	s := unitScheme(t)
	unsupported := legacySource("*")
	unsupported.Spec.Match.Any[0].Selector = &metav1.LabelSelector{MatchLabels: map[string]string{"app": "cilium"}}
	for name, src := range map[string]*kyvernov2.PolicyException{
		"lossy":       legacySource("run-as-non-root"),
		"unsupported": unsupported,
	} {
		t.Run(name, func(t *testing.T) {
			// arrange
			c := fake.NewClientBuilder().WithScheme(s).WithObjects(nonrootPolicy(), src,
				existingBridge(ownLabels, "giantswarm/cilium"), legacyCRD()).Build()
			written, _ := getBridge(t, c)
			removedBefore := testutil.ToFloat64(BridgesRemoved)

			// act
			err := reconcileSource(t, newReconciler(c, c))

			// assert
			require.NoError(t, err)
			bridge, ok := getBridge(t, c)
			require.True(t, ok, "bridge of a %s source removed", name)
			assert.Equal(t, written.ResourceVersion, bridge.ResourceVersion, "bridge of a %s source rewritten", name)
			assert.Zero(t, testutil.ToFloat64(BridgesRemoved)-removedBefore, "bridges_removed_total grew")
		})
	}
}

// Exact source -> bridge; source edited to be lossy -> bridge kept unchanged, state lossy;
// source deleted -> bridge removed.
func TestReconcileBridgeLifecycle(t *testing.T) {
	ctx := context.Background()
	s := unitScheme(t)
	src := legacySource("run-as-non-root", "autogen-run-as-non-root")
	c := fake.NewClientBuilder().WithScheme(s).WithObjects(nonrootPolicy(), src, legacyCRD()).Build()
	r := newReconciler(c, c)

	// exact source: bridge written
	require.NoError(t, reconcileSource(t, r))
	written, ok := getBridge(t, c)
	require.True(t, ok, "bridge not created")

	// source turns lossy: bridge kept unchanged
	require.NoError(t, c.Get(ctx, sourceKey, src))
	src.Spec.Exceptions[0].RuleNames = []string{"run-as-non-root"}
	require.NoError(t, c.Update(ctx, src))

	require.NoError(t, reconcileSource(t, r))

	kept, ok := getBridge(t, c)
	require.True(t, ok, "bridge removed after the source turned lossy")
	assert.Equal(t, written.ResourceVersion, kept.ResourceVersion, "bridge changed after the source turned lossy")
	status, err := migration.Evaluate(ctx, c, testBridgeNamespace, src)
	require.NoError(t, err)
	assert.Equal(t, migration.StateLossy, status.State)
	assert.Equal(t, migration.ReasonRuleNames, status.Reason)
	assert.True(t, status.OwnsBridge(src), "bridge no longer owned by the source")

	// source deleted: bridge removed
	require.NoError(t, c.Delete(ctx, src))

	require.NoError(t, reconcileSource(t, r))

	_, ok = getBridge(t, c)
	assert.False(t, ok, "bridge of a deleted source kept")
}

// The ClusterPolicy was replaced by a ValidatingPolicy of the same name: ruleNames are not checked.
func TestReconcileBridgesThroughCELPolicy(t *testing.T) {
	// arrange
	s := unitScheme(t)
	c := fake.NewClientBuilder().WithScheme(s).WithObjects(
		&policiesv1.ValidatingPolicy{ObjectMeta: metav1.ObjectMeta{Name: testPolicyName}},
		legacySource("run-as-non-root")).Build()

	// act
	err := reconcileSource(t, newReconciler(c, c))

	// assert
	require.NoError(t, err)
	bridge, ok := getBridge(t, c)
	require.True(t, ok, "no bridge for a source whose policy is a ValidatingPolicy")
	assert.Equal(t, []string{testPolicyName}, bridge.Spec.Policies)
}

func TestReconcileWritesNothingWithoutAnyPolicy(t *testing.T) {
	// arrange
	s := unitScheme(t)
	src := legacySource("run-as-non-root")
	c := fake.NewClientBuilder().WithScheme(s).WithObjects(src).Build()

	// act
	err := reconcileSource(t, newReconciler(c, c))

	// assert
	require.NoError(t, err)
	_, ok := getBridge(t, c)
	assert.False(t, ok, "bridged a source whose policy does not exist")
	status, err := migration.Evaluate(context.Background(), c, testBridgeNamespace, src)
	require.NoError(t, err)
	assert.Equal(t, migration.StatePending, status.State)
	assert.Equal(t, migration.ReasonPolicyNotFound, status.Reason)
}

// "default/cilium" sorts before "giantswarm/cilium" (sourceKey).
func TestReconcileSameNameTieBreak(t *testing.T) {
	ctx := context.Background()
	s := unitScheme(t)
	lower := legacySource("*")
	lower.Namespace = "default"
	lowerKey := client.ObjectKeyFromObject(lower)
	reconcileAll := func(t *testing.T, r *LegacyExceptionReconciler, keys []types.NamespacedName) {
		t.Helper()
		for _, key := range keys {
			_, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: key})
			require.NoError(t, err)
		}
	}

	for name, keys := range map[string][]types.NamespacedName{
		"higher reconciled first": {sourceKey, lowerKey},
		"lower reconciled first":  {lowerKey, sourceKey},
	} {
		t.Run("no bridge yet, "+name, func(t *testing.T) {
			// arrange
			c := fake.NewClientBuilder().WithScheme(s).WithObjects(nonrootPolicy(), legacySource("*"), lower.DeepCopy()).Build()

			// act
			reconcileAll(t, newReconciler(c, c), keys)

			// assert
			bridge, ok := getBridge(t, c)
			require.True(t, ok, "no bridge written")
			assert.Equal(t, "default/cilium", bridge.Annotations[migration.AnnotationMigratedFrom],
				"bridge not owned by the lowest-sorting source")
		})
		t.Run("existing bridge of the higher source, "+name, func(t *testing.T) {
			// arrange
			c := fake.NewClientBuilder().WithScheme(s).WithObjects(nonrootPolicy(), legacySource("*"), lower.DeepCopy(),
				existingBridge(ownLabels, "giantswarm/cilium")).Build()

			// act
			reconcileAll(t, newReconciler(c, c), keys)

			// assert
			bridge, ok := getBridge(t, c)
			require.True(t, ok, "bridge removed")
			assert.Equal(t, "giantswarm/cilium", bridge.Annotations[migration.AnnotationMigratedFrom], "bridge ownership moved")
		})
	}
}

func TestReconcileRemovesBridgeWhenSourceDeleted(t *testing.T) {
	// arrange
	s := unitScheme(t)
	c := fake.NewClientBuilder().WithScheme(s).WithObjects(existingBridge(ownLabels, "giantswarm/cilium"), legacyCRD()).Build()
	removedBefore := testutil.ToFloat64(BridgesRemoved)

	// act
	err := reconcileSource(t, newReconciler(c, c))

	// assert
	require.NoError(t, err)
	_, ok := getBridge(t, c)
	assert.False(t, ok, "bridge of a deleted source kept")
	assert.Equal(t, 1.0, testutil.ToFloat64(BridgesRemoved)-removedBefore, "bridges_removed_total growth")
}

func TestReconcileKeepsBridgeWhenDeletionIsNotConfirmed(t *testing.T) {
	s := unitScheme(t)
	now := metav1.Now()
	terminating := legacyCRD()
	terminating.DeletionTimestamp, terminating.Finalizers = &now, []string{"customresourcecleanup.apiextensions.k8s.io"}
	notServed := legacyCRD()
	notServed.Spec.Versions[0].Served = false
	failingGet := interceptor.Funcs{Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
		return errors.New("connection refused")
	}}

	cases := map[string]struct {
		api     client.Client
		wantErr bool
	}{
		"source still on the API server": {api: fake.NewClientBuilder().WithScheme(s).WithObjects(legacyCRD(), legacySource("*")).Build()},
		"legacy CRD being deleted":       {api: fake.NewClientBuilder().WithScheme(s).WithObjects(terminating).Build()},
		"legacy CRD not found":           {api: fake.NewClientBuilder().WithScheme(s).Build()},
		"legacy CRD does not serve v2":   {api: fake.NewClientBuilder().WithScheme(s).WithObjects(notServed).Build()},
		"API server unreachable":         {api: fake.NewClientBuilder().WithScheme(s).WithInterceptorFuncs(failingGet).Build(), wantErr: true},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			// arrange
			cached := fake.NewClientBuilder().WithScheme(s).WithObjects(existingBridge(ownLabels, "giantswarm/cilium")).Build()

			// act
			err := reconcileSource(t, newReconciler(cached, tc.api))

			// assert
			if tc.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
			_, ok := getBridge(t, cached)
			assert.True(t, ok, "bridge deleted without confirmation")
		})
	}
}

// The cache still holds the checked bridge while the API server has a new object of that name.
func TestReconcileKeepsBridgeRecreatedBeforeDelete(t *testing.T) {
	// arrange
	s := unitScheme(t)
	recreated := existingBridge(ownLabels, "giantswarm/cilium")
	recreated.UID = "recreated"
	funcs := interceptor.Funcs{
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
	cached := fake.NewClientBuilder().WithScheme(s).WithObjects(recreated).WithInterceptorFuncs(funcs).Build()
	api := fake.NewClientBuilder().WithScheme(s).WithObjects(legacyCRD()).Build()
	removedBefore := testutil.ToFloat64(BridgesRemoved)

	// act
	err := reconcileSource(t, newReconciler(cached, api))

	// assert
	assert.True(t, apierrors.IsConflict(err), "got error %v, want a conflict", err)
	_, ok := getBridge(t, cached)
	assert.True(t, ok, "bridge recreated after the check was deleted")
	assert.Zero(t, testutil.ToFloat64(BridgesRemoved)-removedBefore, "bridges_removed_total grew")
}

// Neither a ClusterPolicy nor a CEL policy of that name exists any more: the bridge stays as written.
func TestReconcileKeepsBridgeWhenPolicyGone(t *testing.T) {
	// arrange
	s := unitScheme(t)
	c := fake.NewClientBuilder().WithScheme(s).WithObjects(
		legacySource("run-as-non-root", "autogen-run-as-non-root"), existingBridge(ownLabels, "giantswarm/cilium")).Build()

	// act
	err := reconcileSource(t, newReconciler(c, c))

	// assert
	require.NoError(t, err)
	bridge, ok := getBridge(t, c)
	require.True(t, ok, "bridge removed after its policy went away")
	assert.Equal(t, []string{stalePolicy}, bridge.Spec.Policies, "bridge rewritten after its policy went away")
}

// Evaluate saw no bridge; by the time the bridge is written, someone else created that name.
func TestReconcileNeverTakesOverObjectCreatedAfterEvaluate(t *testing.T) {
	// arrange
	s := unitScheme(t)
	bridgeGets := 0
	hideFirstBridgeGet := interceptor.Funcs{Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
		if _, ok := obj.(*policyAPI.PolicyException); ok {
			bridgeGets++
			if bridgeGets == 1 {
				return apierrors.NewNotFound(policyAPI.GroupVersion.WithResource("policyexceptions").GroupResource(), key.Name)
			}
		}
		return c.Get(ctx, key, obj, opts...)
	}}
	c := fake.NewClientBuilder().WithScheme(s).WithObjects(nonrootPolicy(), legacySource("*"),
		existingBridge(nil, "")).WithInterceptorFuncs(hideFirstBridgeGet).Build()
	applyFailedBefore := testutil.ToFloat64(TranslationErrors.WithLabelValues(ReasonApplyFailed))

	// act
	err := reconcileSource(t, newReconciler(c, c))

	// assert
	require.NoError(t, err, "a name collision is not an error")
	bridge, ok := getBridge(t, c)
	require.True(t, ok)
	assert.Equal(t, []string{stalePolicy}, bridge.Spec.Policies, "object created by someone else was rewritten")
	assert.Empty(t, bridge.Labels[migration.ManagedByLabel], "object created by someone else was taken over")
	assert.Zero(t, testutil.ToFloat64(TranslationErrors.WithLabelValues(ReasonApplyFailed))-applyFailedBefore, "apply_failed grew")
}

func TestReconcileWriteFailure(t *testing.T) {
	// arrange
	s := unitScheme(t)
	boom := errors.New("boom")
	c := fake.NewClientBuilder().WithScheme(s).WithObjects(nonrootPolicy(), legacySource("*")).
		WithInterceptorFuncs(interceptor.Funcs{Create: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
			return boom
		}}).Build()
	applyFailedBefore := testutil.ToFloat64(TranslationErrors.WithLabelValues(ReasonApplyFailed))

	// act
	err := reconcileSource(t, newReconciler(c, c))

	// assert
	assert.ErrorIs(t, err, boom)
	assert.Equal(t, 1.0, testutil.ToFloat64(TranslationErrors.WithLabelValues(ReasonApplyFailed))-applyFailedBefore, "apply_failed growth")
}

func TestReconcileSourceGetFails(t *testing.T) {
	// arrange
	s := unitScheme(t)
	boom := errors.New("boom")
	cached := fake.NewClientBuilder().WithScheme(s).WithObjects(existingBridge(ownLabels, "giantswarm/cilium"), legacyCRD()).
		WithInterceptorFuncs(interceptor.Funcs{Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
			if _, ok := obj.(*kyvernov2.PolicyException); ok {
				return boom
			}
			return c.Get(ctx, key, obj, opts...)
		}}).Build()
	// The API reader has no source either: only the failed cache read may stop the delete.
	api := fake.NewClientBuilder().WithScheme(s).WithObjects(legacyCRD()).Build()

	// act
	err := reconcileSource(t, newReconciler(cached, api))

	// assert
	assert.ErrorIs(t, err, boom)
	_, ok := getBridge(t, cached)
	assert.True(t, ok, "bridge deleted after the source could not be read")
}

func TestReconcileEvaluateFails(t *testing.T) {
	// arrange
	s := unitScheme(t)
	boom := errors.New("boom")
	c := fake.NewClientBuilder().WithScheme(s).WithObjects(nonrootPolicy(), legacySource("run-as-non-root")).
		WithInterceptorFuncs(interceptor.Funcs{Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
			if _, ok := obj.(*kyvernov1.ClusterPolicy); ok {
				return boom
			}
			return c.Get(ctx, key, obj, opts...)
		}}).Build()
	lookupFailedBefore := testutil.ToFloat64(TranslationErrors.WithLabelValues(ReasonLookupFailed))

	// act
	err := reconcileSource(t, newReconciler(c, c))

	// assert
	assert.ErrorIs(t, err, boom)
	_, ok := getBridge(t, c)
	assert.False(t, ok, "bridge written although the policy lookup failed")
	assert.Equal(t, 1.0, testutil.ToFloat64(TranslationErrors.WithLabelValues(ReasonLookupFailed))-lookupFailedBefore, "lookup_failed growth")
}

func TestReconcileBridgeGetFailsWhenSourceGone(t *testing.T) {
	// arrange
	s := unitScheme(t)
	boom := errors.New("boom")
	c := fake.NewClientBuilder().WithScheme(s).WithObjects(existingBridge(ownLabels, "giantswarm/cilium"), legacyCRD()).
		WithInterceptorFuncs(interceptor.Funcs{Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
			if _, ok := obj.(*policyAPI.PolicyException); ok {
				return boom
			}
			return c.Get(ctx, key, obj, opts...)
		}}).Build()

	// act
	err := reconcileSource(t, newReconciler(c, c))

	// assert
	assert.ErrorIs(t, err, boom)
}

func TestReconcileDeleteOutcomes(t *testing.T) {
	s := unitScheme(t)
	boom := errors.New("boom")
	cases := map[string]struct {
		deleteErr       error
		wantErr         error
		wantRemoved     float64
		wantDeleteFails float64
	}{
		"already deleted by someone else": {
			deleteErr: apierrors.NewNotFound(policyAPI.GroupVersion.WithResource("policyexceptions").GroupResource(), bridgeKey.Name),
		},
		"delete fails": {deleteErr: boom, wantErr: boom, wantDeleteFails: 1},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			// arrange
			c := fake.NewClientBuilder().WithScheme(s).WithObjects(existingBridge(ownLabels, "giantswarm/cilium"), legacyCRD()).
				WithInterceptorFuncs(interceptor.Funcs{Delete: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.DeleteOption) error {
					return tc.deleteErr
				}}).Build()
			removedBefore := testutil.ToFloat64(BridgesRemoved)
			deleteFailedBefore := testutil.ToFloat64(TranslationErrors.WithLabelValues(ReasonDeleteFailed))

			// act
			err := reconcileSource(t, newReconciler(c, c))

			// assert
			if tc.wantErr != nil {
				assert.ErrorIs(t, err, tc.wantErr)
			} else {
				assert.NoError(t, err)
			}
			assert.Equal(t, tc.wantRemoved, testutil.ToFloat64(BridgesRemoved)-removedBefore, "bridges_removed_total growth")
			assert.Equal(t, tc.wantDeleteFails,
				testutil.ToFloat64(TranslationErrors.WithLabelValues(ReasonDeleteFailed))-deleteFailedBefore, "delete_failed growth")
		})
	}
}

func TestBridgeToSource(t *testing.T) {
	cases := map[string]struct {
		labels      map[string]string
		annotations map[string]string
		want        []reconcile.Request
	}{
		"own bridge": {labels: ownLabels, annotations: map[string]string{migration.AnnotationMigratedFrom: "giantswarm/cilium"},
			want: []reconcile.Request{{NamespacedName: sourceKey}}},
		"no managed-by label": {annotations: map[string]string{migration.AnnotationMigratedFrom: "giantswarm/cilium"}},
		"managed by kyverno-policy-operator": {labels: map[string]string{migration.ManagedByLabel: migration.KPOComponentName},
			annotations: map[string]string{migration.AnnotationMigratedFrom: "giantswarm/cilium"}},
		"no migrated-from annotation":        {labels: ownLabels},
		"malformed migrated-from annotation": {labels: ownLabels, annotations: map[string]string{migration.AnnotationMigratedFrom: "cilium"}},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			// arrange
			obj := &policyAPI.PolicyException{ObjectMeta: metav1.ObjectMeta{
				Name: bridgeKey.Name, Namespace: bridgeKey.Namespace, Labels: tc.labels, Annotations: tc.annotations,
			}}

			// act
			got := bridgeToSource(context.Background(), obj)

			// assert
			assert.Equal(t, tc.want, got)
		})
	}
}
