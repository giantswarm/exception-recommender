package controller

import (
	"context"
	"errors"
	"time"

	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"
)

// BridgeCRDCheckInterval is how often a BridgeCRDWatcher checks whether the bridge CRDs are served.
var BridgeCRDCheckInterval = time.Minute

// ErrBridgeCRDsAvailable stops the manager once the bridge CRDs are served, so the pod restarts
// with migration bridges enabled.
var ErrBridgeCRDsAvailable = errors.New("migration bridge CRDs are now available, restarting to enable bridges")

// bridgeCRDs are the kinds the migration bridges need: kyverno.io/v2 PolicyException and
// kyverno.io/v1 ClusterPolicy, which Kyverno 1.20 removes, and policy.giantswarm.io/v1alpha1
// PolicyException, which the bridges are.
var bridgeCRDs = []schema.GroupVersionKind{
	{Group: "kyverno.io", Version: "v2", Kind: "PolicyException"},
	{Group: "kyverno.io", Version: "v1", Kind: "ClusterPolicy"},
	{Group: "policy.giantswarm.io", Version: "v1alpha1", Kind: "PolicyException"},
}

// MissingBridgeCRDs returns the bridge CRDs the API server does not serve. It builds a new
// RESTMapper on every call, so a kind that was missing before is found once it is served.
func MissingBridgeCRDs(cfg *rest.Config) ([]string, error) {
	httpClient, err := rest.HTTPClientFor(cfg)
	if err != nil {
		return nil, err
	}
	mapper, err := apiutil.NewDynamicRESTMapper(cfg, httpClient)
	if err != nil {
		return nil, err
	}
	return missingKinds(mapper, bridgeCRDs)
}

// missingKinds returns the kinds the mapper has no mapping for. Any error other than NoMatch is
// returned.
func missingKinds(mapper meta.RESTMapper, gvks []schema.GroupVersionKind) ([]string, error) {
	var missing []string
	for _, gvk := range gvks {
		_, err := mapper.RESTMapping(gvk.GroupKind(), gvk.Version)
		if meta.IsNoMatchError(err) {
			missing = append(missing, gvk.String())
			continue
		}
		if err != nil {
			return nil, err
		}
	}
	return missing, nil
}

// BridgeCRDWatcher runs while migration bridges are enabled but were skipped at startup because a
// CRD was missing. Once every bridge CRD is served, Start returns ErrBridgeCRDsAvailable, which
// stops the manager so the pod restarts with bridges enabled.
type BridgeCRDWatcher struct {
	// Check returns the bridge CRDs that are not served, see MissingBridgeCRDs.
	Check    func() ([]string, error)
	Interval time.Duration
	Log      logr.Logger
}

func (w *BridgeCRDWatcher) Start(ctx context.Context) error {
	ticker := time.NewTicker(w.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
		missing, err := w.Check()
		if err != nil {
			w.Log.Error(err, "unable to check for the migration bridge CRDs")
			continue
		}
		if len(missing) > 0 {
			w.Log.V(1).Info("migration bridge CRDs still not served", "missing", missing)
			continue
		}
		w.Log.Info("migration bridge CRDs are now available, restarting to enable bridges")
		return ErrBridgeCRDsAvailable
	}
}

// NeedLeaderElection is false because every replica has to restart to set up the bridges.
func (w *BridgeCRDWatcher) NeedLeaderElection() bool {
	return false
}
