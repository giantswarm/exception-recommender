package controller

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/go-logr/logr"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/event"

	"github.com/giantswarm/exception-recommender/internal/migration"
)

func TestResyncerQueuesSourcesAndStampsTime(t *testing.T) {
	s := unitScheme(t)
	kpo := named(legacySource("*"), "generated")
	kpo.Labels = map[string]string{migration.ManagedByLabel: migration.KPOComponentName}
	api := fake.NewClientBuilder().WithScheme(s).WithObjects(legacySource("*"), kpo).Build()
	events := make(chan event.GenericEvent, 10)
	LastResync.Set(0)

	(&Resyncer{APIReader: api, Events: events, Interval: time.Hour, Log: logr.Discard()}).resync(context.Background())

	if len(events) != 1 {
		t.Fatalf("queued %d events, want 1", len(events))
	}
	if testutil.ToFloat64(LastResync) == 0 {
		t.Fatal("last resync timestamp not set")
	}
}

func TestResyncerKeepsTimestampWhenListFails(t *testing.T) {
	s := unitScheme(t)
	api := fake.NewClientBuilder().WithScheme(s).WithInterceptorFuncs(interceptor.Funcs{
		List: func(ctx context.Context, c client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
			return errors.New("the server could not find the requested resource")
		},
	}).Build()
	events := make(chan event.GenericEvent, 10)
	LastResync.Set(0)

	(&Resyncer{APIReader: api, Events: events, Interval: time.Hour, Log: logr.Discard()}).resync(context.Background())

	if len(events) != 0 || testutil.ToFloat64(LastResync) != 0 {
		t.Fatalf("resync after a failed list: %d events, timestamp %v", len(events), testutil.ToFloat64(LastResync))
	}
}

// failingMapper fails every lookup with an error that is not a NoMatch.
type failingMapper struct{ meta.RESTMapper }

func (failingMapper) RESTMapping(schema.GroupKind, ...string) (*meta.RESTMapping, error) {
	return nil, errors.New("the server is currently unable to handle the request")
}

func TestMissingBridgeCRDs(t *testing.T) {
	policyException := schema.GroupVersionKind{Group: "kyverno.io", Version: "v2", Kind: "PolicyException"}
	clusterPolicy := schema.GroupVersionKind{Group: "kyverno.io", Version: "v1", Kind: "ClusterPolicy"}
	gsPolicyException := schema.GroupVersionKind{Group: "policy.giantswarm.io", Version: "v1alpha1", Kind: "PolicyException"}
	served := func(kinds ...schema.GroupVersionKind) meta.RESTMapper {
		mapper := meta.NewDefaultRESTMapper(nil)
		for _, gvk := range kinds {
			mapper.Add(gvk, meta.RESTScopeNamespace)
		}
		return mapper
	}
	for name, tc := range map[string]struct {
		mapper  meta.RESTMapper
		want    []string
		wantErr bool
	}{
		"all served":                     {mapper: served(policyException, clusterPolicy, gsPolicyException)},
		"no legacy PolicyException":      {mapper: served(clusterPolicy, gsPolicyException), want: []string{policyException.String()}},
		"no ClusterPolicy":               {mapper: served(policyException, gsPolicyException), want: []string{clusterPolicy.String()}},
		"no Giant Swarm PolicyException": {mapper: served(policyException, clusterPolicy), want: []string{gsPolicyException.String()}},
		"none served":                    {mapper: served(), want: []string{policyException.String(), clusterPolicy.String(), gsPolicyException.String()}},
		"discovery error":                {mapper: failingMapper{}, wantErr: true},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := MissingBridgeCRDs(tc.mapper)
			if (err != nil) != tc.wantErr || !slices.Equal(got, tc.want) {
				t.Fatalf("got %v, %v; want %v, error %v", got, err, tc.want, tc.wantErr)
			}
		})
	}
}
