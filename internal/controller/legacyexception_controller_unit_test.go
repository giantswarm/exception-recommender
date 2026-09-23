package controller

import (
	"context"
	"errors"
	"testing"

	policyAPI "github.com/giantswarm/policy-api/api/v1alpha1"
	policiesv1 "github.com/kyverno/api/api/policies.kyverno.io/v1"
	kyvernov2 "github.com/kyverno/kyverno/api/kyverno/v2"
	"github.com/prometheus/client_golang/prometheus/testutil"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

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
	if err != nil {
		t.Fatal(err)
	}
	return &bridge, true
}

func TestReconcileWritesBridge(t *testing.T) {
	s := unitScheme(t)
	c := fake.NewClientBuilder().WithScheme(s).
		WithObjects(nonrootPolicy(), legacySource("run-as-non-root", "autogen-run-as-non-root")).Build()

	if err := reconcileSource(t, newReconciler(c, c)); err != nil {
		t.Fatal(err)
	}
	bridge, ok := getBridge(t, c)
	if !ok {
		t.Fatal("bridge not created")
	}
	if bridge.Labels[migration.ManagedByLabel] != migration.ComponentName ||
		bridge.Annotations[migration.AnnotationMigratedFrom] != "giantswarm/cilium" {
		t.Fatalf("wrong metadata: %v %v", bridge.Labels, bridge.Annotations)
	}
	if len(bridge.Spec.Policies) != 1 || len(bridge.Spec.Targets) != 2 || len(bridge.OwnerReferences) != 0 {
		t.Fatalf("wrong bridge: %+v", bridge)
	}
}

func TestReconcileUpdatesOwnBridge(t *testing.T) {
	s := unitScheme(t)
	c := fake.NewClientBuilder().WithScheme(s).WithObjects(nonrootPolicy(),
		legacySource("*"), existingBridge(ownLabels, "giantswarm/cilium")).Build()

	if err := reconcileSource(t, newReconciler(c, c)); err != nil {
		t.Fatal(err)
	}
	bridge, _ := getBridge(t, c)
	if bridge.Spec.Policies[0] != testPolicyName {
		t.Fatalf("bridge not updated: %+v", bridge.Spec)
	}
}

func TestReconcileNeverTouchesUnlabelledGSPolex(t *testing.T) {
	s := unitScheme(t)
	for name, objects := range map[string][]client.Object{
		"source exists": {nonrootPolicy(), legacySource("*"), existingBridge(nil, "giantswarm/cilium"), legacyCRD()},
		"source gone":   {existingBridge(nil, "giantswarm/cilium"), legacyCRD()},
	} {
		t.Run(name, func(t *testing.T) {
			c := fake.NewClientBuilder().WithScheme(s).WithObjects(objects...).Build()
			if err := reconcileSource(t, newReconciler(c, c)); err != nil {
				t.Fatal(err)
			}
			bridge, ok := getBridge(t, c)
			if !ok || bridge.Spec.Policies[0] != stalePolicy {
				t.Fatalf("unlabelled gspolex was changed or deleted: %+v", bridge)
			}
		})
	}
}

func TestReconcileSkipsKPOManagedSource(t *testing.T) {
	s := unitScheme(t)
	src := legacySource("*")
	src.Labels = map[string]string{migration.ManagedByLabel: migration.KPOComponentName}
	c := fake.NewClientBuilder().WithScheme(s).WithObjects(nonrootPolicy(), src).Build()

	if err := reconcileSource(t, newReconciler(c, c)); err != nil {
		t.Fatal(err)
	}
	if _, ok := getBridge(t, c); ok {
		t.Fatal("bridged a kyverno-policy-operator exception")
	}
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
			c := fake.NewClientBuilder().WithScheme(s).WithObjects(nonrootPolicy(), src,
				existingBridge(ownLabels, "giantswarm/cilium"), legacyCRD()).Build()
			written, _ := getBridge(t, c)
			before := testutil.ToFloat64(BridgesRemoved)

			if err := reconcileSource(t, newReconciler(c, c)); err != nil {
				t.Fatal(err)
			}
			bridge, ok := getBridge(t, c)
			if !ok || bridge.ResourceVersion != written.ResourceVersion {
				t.Fatalf("bridge of a %s source removed or rewritten: %+v", name, bridge)
			}
			if got := testutil.ToFloat64(BridgesRemoved) - before; got != 0 {
				t.Fatalf("bridges_removed_total grew by %v, want 0", got)
			}
		})
	}
}

func TestReconcileBridgeLifecycle(t *testing.T) {
	// Exact source -> bridge; source edited to be lossy -> bridge kept unchanged, state lossy;
	// source deleted -> bridge removed.
	ctx := context.Background()
	s := unitScheme(t)
	src := legacySource("run-as-non-root", "autogen-run-as-non-root")
	c := fake.NewClientBuilder().WithScheme(s).WithObjects(nonrootPolicy(), src, legacyCRD()).Build()
	r := newReconciler(c, c)

	if err := reconcileSource(t, r); err != nil {
		t.Fatal(err)
	}
	written, ok := getBridge(t, c)
	if !ok {
		t.Fatal("bridge not created")
	}

	if err := c.Get(ctx, sourceKey, src); err != nil {
		t.Fatal(err)
	}
	src.Spec.Exceptions[0].RuleNames = []string{"run-as-non-root"}
	if err := c.Update(ctx, src); err != nil {
		t.Fatal(err)
	}
	if err := reconcileSource(t, r); err != nil {
		t.Fatal(err)
	}
	kept, ok := getBridge(t, c)
	if !ok || kept.ResourceVersion != written.ResourceVersion {
		t.Fatalf("bridge removed or changed after the source turned lossy: %+v", kept)
	}
	status, err := migration.Evaluate(ctx, c, testBridgeNamespace, src)
	if err != nil {
		t.Fatal(err)
	}
	if status.State != migration.StateLossy || status.Reason != migration.ReasonRuleNames || !status.OwnsBridge(src) {
		t.Fatalf("got state %q reason %q owns bridge %v, want lossy rule_names with the bridge kept",
			status.State, status.Reason, status.OwnsBridge(src))
	}

	if err := c.Delete(ctx, src); err != nil {
		t.Fatal(err)
	}
	if err := reconcileSource(t, r); err != nil {
		t.Fatal(err)
	}
	if _, ok := getBridge(t, c); ok {
		t.Fatal("bridge of a deleted source kept")
	}
}

func TestReconcileBridgesThroughCELPolicy(t *testing.T) {
	// The ClusterPolicy was replaced by a ValidatingPolicy of the same name: ruleNames are not checked.
	s := unitScheme(t)
	c := fake.NewClientBuilder().WithScheme(s).WithObjects(
		&policiesv1.ValidatingPolicy{ObjectMeta: metav1.ObjectMeta{Name: testPolicyName}},
		legacySource("run-as-non-root")).Build()

	if err := reconcileSource(t, newReconciler(c, c)); err != nil {
		t.Fatal(err)
	}
	bridge, ok := getBridge(t, c)
	if !ok || bridge.Spec.Policies[0] != testPolicyName {
		t.Fatalf("no bridge for a source whose policy is a ValidatingPolicy: %+v", bridge)
	}
}

func TestReconcileWritesNothingWithoutAnyPolicy(t *testing.T) {
	s := unitScheme(t)
	src := legacySource("run-as-non-root")
	c := fake.NewClientBuilder().WithScheme(s).WithObjects(src).Build()

	if err := reconcileSource(t, newReconciler(c, c)); err != nil {
		t.Fatal(err)
	}
	if _, ok := getBridge(t, c); ok {
		t.Fatal("bridged a source whose policy does not exist")
	}
	status, err := migration.Evaluate(context.Background(), c, testBridgeNamespace, src)
	if err != nil {
		t.Fatal(err)
	}
	if status.State != migration.StatePending || status.Reason != migration.ReasonPolicyNotFound {
		t.Fatalf("got state %q reason %q, want pending policy_not_found", status.State, status.Reason)
	}
}

func TestReconcileSameNameTieBreak(t *testing.T) {
	// "default/cilium" sorts before "giantswarm/cilium" (sourceKey).
	ctx := context.Background()
	s := unitScheme(t)
	lower := legacySource("*")
	lower.Namespace = "default"
	reconcileBoth := func(t *testing.T, r *LegacyExceptionReconciler, keys ...types.NamespacedName) {
		t.Helper()
		for _, key := range keys {
			if _, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: key}); err != nil {
				t.Fatal(err)
			}
		}
	}
	lowerKey := client.ObjectKeyFromObject(lower)

	for name, keys := range map[string][]types.NamespacedName{
		"higher reconciled first": {sourceKey, lowerKey},
		"lower reconciled first":  {lowerKey, sourceKey},
	} {
		t.Run("no bridge yet, "+name, func(t *testing.T) {
			c := fake.NewClientBuilder().WithScheme(s).WithObjects(nonrootPolicy(), legacySource("*"), lower.DeepCopy()).Build()
			reconcileBoth(t, newReconciler(c, c), keys...)
			bridge, ok := getBridge(t, c)
			if !ok || bridge.Annotations[migration.AnnotationMigratedFrom] != "default/cilium" {
				t.Fatalf("bridge not owned by the lowest-sorting source: %+v", bridge)
			}
		})
		t.Run("existing bridge of the higher source, "+name, func(t *testing.T) {
			c := fake.NewClientBuilder().WithScheme(s).WithObjects(nonrootPolicy(), legacySource("*"), lower.DeepCopy(),
				existingBridge(ownLabels, "giantswarm/cilium")).Build()
			reconcileBoth(t, newReconciler(c, c), keys...)
			bridge, ok := getBridge(t, c)
			if !ok || bridge.Annotations[migration.AnnotationMigratedFrom] != "giantswarm/cilium" {
				t.Fatalf("bridge ownership moved: %+v", bridge)
			}
		})
	}
}

func TestReconcileRemovesBridgeWhenSourceDeleted(t *testing.T) {
	s := unitScheme(t)
	c := fake.NewClientBuilder().WithScheme(s).WithObjects(existingBridge(ownLabels, "giantswarm/cilium"), legacyCRD()).Build()
	before := testutil.ToFloat64(BridgesRemoved)

	if err := reconcileSource(t, newReconciler(c, c)); err != nil {
		t.Fatal(err)
	}
	if _, ok := getBridge(t, c); ok {
		t.Fatal("bridge of a deleted source kept")
	}
	if got := testutil.ToFloat64(BridgesRemoved) - before; got != 1 {
		t.Fatalf("bridges_removed_total grew by %v, want 1", got)
	}
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
			cached := fake.NewClientBuilder().WithScheme(s).WithObjects(existingBridge(ownLabels, "giantswarm/cilium")).Build()
			err := reconcileSource(t, newReconciler(cached, tc.api))
			if (err != nil) != tc.wantErr {
				t.Fatalf("got error %v, want error %v", err, tc.wantErr)
			}
			if _, ok := getBridge(t, cached); !ok {
				t.Fatal("bridge deleted without confirmation")
			}
		})
	}
}

func TestReconcileKeepsBridgeRecreatedBeforeDelete(t *testing.T) {
	// The cache still holds the checked bridge while the API server has a new object of that name.
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
	before := testutil.ToFloat64(BridgesRemoved)

	if err := reconcileSource(t, newReconciler(cached, api)); !apierrors.IsConflict(err) {
		t.Fatalf("got error %v, want a conflict", err)
	}
	if _, ok := getBridge(t, cached); !ok {
		t.Fatal("bridge recreated after the check was deleted")
	}
	if got := testutil.ToFloat64(BridgesRemoved) - before; got != 0 {
		t.Fatalf("bridges_removed_total grew by %v, want 0", got)
	}
}

func TestReconcileKeepsBridgeWhenPolicyGone(t *testing.T) {
	// Neither a ClusterPolicy nor a CEL policy of that name exists any more: the bridge stays as written.
	s := unitScheme(t)
	c := fake.NewClientBuilder().WithScheme(s).WithObjects(
		legacySource("run-as-non-root", "autogen-run-as-non-root"), existingBridge(ownLabels, "giantswarm/cilium")).Build()

	if err := reconcileSource(t, newReconciler(c, c)); err != nil {
		t.Fatal(err)
	}
	bridge, ok := getBridge(t, c)
	if !ok || bridge.Spec.Policies[0] != stalePolicy {
		t.Fatalf("bridge removed or rewritten after its policy went away: %+v", bridge)
	}
}
