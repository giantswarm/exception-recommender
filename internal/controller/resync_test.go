package controller

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/go-logr/logr"
	"github.com/prometheus/client_golang/prometheus/testutil"
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
