package controller

import (
	"context"
	"time"

	"github.com/go-logr/logr"
	kyvernov2 "github.com/kyverno/kyverno/api/kyverno/v2"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"

	"github.com/giantswarm/exception-recommender/internal/migration"
)

// ResyncInterval is how often every legacy PolicyException is re-evaluated, which picks up
// ClusterPolicy changes that make a translation lossy or exact.
var ResyncInterval = 5 * time.Minute

// Resyncer periodically lists legacy PolicyExceptions from the API server and queues them for the
// LegacyExceptionReconciler. A failed listing leaves LastResync unchanged.
type Resyncer struct {
	APIReader client.Reader
	Events    chan<- event.GenericEvent
	Interval  time.Duration
	Log       logr.Logger
}

func (r *Resyncer) Start(ctx context.Context) error {
	ticker := time.NewTicker(r.Interval)
	defer ticker.Stop()
	for {
		r.resync(ctx)
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

func (r *Resyncer) resync(ctx context.Context) {
	var sources kyvernov2.PolicyExceptionList
	if err := r.APIReader.List(ctx, &sources); err != nil {
		r.Log.Error(err, "unable to list legacy PolicyExceptions, resync skipped")
		return
	}
	queued := 0
	for i := range sources.Items {
		if migration.IsManagedByKPO(&sources.Items[i]) {
			continue
		}
		select {
		case r.Events <- event.GenericEvent{Object: &sources.Items[i]}:
			queued++
		case <-ctx.Done():
			return
		}
	}
	LastResync.SetToCurrentTime()
	r.Log.V(1).Info("resync queued legacy PolicyExceptions", "count", queued)
}

// LegacyCRDsPresent reports whether the API server serves kyverno.io/v2 PolicyException and
// kyverno.io/v1 ClusterPolicy. Kyverno 1.20 removes both.
func LegacyCRDsPresent(mapper meta.RESTMapper) (bool, error) {
	for _, gvk := range []schema.GroupVersionKind{
		schema.GroupVersionKind{Group: "kyverno.io", Version: "v2", Kind: "PolicyException"},
		schema.GroupVersionKind{Group: "kyverno.io", Version: "v1", Kind: "ClusterPolicy"},
	} {
		_, err := mapper.RESTMapping(gvk.GroupKind(), gvk.Version)
		if meta.IsNoMatchError(err) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
	}
	return true, nil
}
