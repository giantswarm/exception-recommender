package controller

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/go-logr/logr"
	"github.com/stretchr/testify/assert"
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

			if tc.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestBridgeCRDWatcherStopsOnceCRDsAreServed(t *testing.T) {
	// arrange
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

	// act
	err := w.Start(context.Background())

	// assert
	assert.ErrorIs(t, err, ErrBridgeCRDsAvailable)
	assert.Equal(t, len(results), checks, "a failed or incomplete check must not stop the watcher")
}

func TestBridgeCRDWatcherStopsWithTheManager(t *testing.T) {
	// arrange
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	w := &BridgeCRDWatcher{
		Check:    func() ([]string, error) { return []string{"missing"}, nil },
		Interval: time.Hour,
		Log:      logr.Discard(),
	}

	// act
	err := w.Start(ctx)

	// assert
	assert.NoError(t, err)
}
