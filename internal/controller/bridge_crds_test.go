package controller

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// failingMapper fails every lookup with an error that is not a NoMatch.
type failingMapper struct{ meta.RESTMapper }

func (failingMapper) RESTMapping(schema.GroupKind, ...string) (*meta.RESTMapping, error) {
	return nil, errors.New("the server is currently unable to handle the request")
}

func TestMissingKinds(t *testing.T) {
	policyException, clusterPolicy, gsPolicyException := bridgeCRDs[0], bridgeCRDs[1], bridgeCRDs[2]
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
			got, err := missingKinds(tc.mapper, bridgeCRDs)
			if (err != nil) != tc.wantErr || !slices.Equal(got, tc.want) {
				t.Fatalf("got %v, %v; want %v, error %v", got, err, tc.want, tc.wantErr)
			}
		})
	}
}

func TestBridgeCRDWatcherStopsOnceCRDsAreServed(t *testing.T) {
	results := []struct {
		missing []string
		err     error
	}{
		{err: errors.New("discovery failed")},
		{missing: []string{"kyverno.io/v2, Kind=PolicyException"}},
		{},
	}
	checks := 0
	w := &BridgeCRDWatcher{
		Check: func() ([]string, error) {
			result := results[checks]
			checks++
			return result.missing, result.err
		},
		Interval: time.Millisecond,
		Log:      logr.Discard(),
	}
	if err := w.Start(context.Background()); !errors.Is(err, ErrBridgeCRDsAvailable) {
		t.Fatalf("got %v, want %v", err, ErrBridgeCRDsAvailable)
	}
	if checks != len(results) {
		t.Fatalf("got %d checks, want %d", checks, len(results))
	}
}

func TestBridgeCRDWatcherStopsWithTheManager(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	w := &BridgeCRDWatcher{
		Check:    func() ([]string, error) { return []string{"missing"}, nil },
		Interval: time.Hour,
		Log:      logr.Discard(),
	}
	if err := w.Start(ctx); err != nil {
		t.Fatalf("got %v, want nil", err)
	}
}
