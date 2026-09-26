package migration

import (
	"testing"

	"k8s.io/apimachinery/pkg/types"
)

func TestParseSourceKey(t *testing.T) {
	cases := []struct {
		value  string
		want   types.NamespacedName
		wantOK bool
	}{
		{value: "giantswarm/cilium", want: types.NamespacedName{Namespace: "giantswarm", Name: "cilium"}, wantOK: true},
		{value: ""},
		{value: "cilium"},
		{value: "/cilium"},
		{value: "giantswarm/"},
		{value: "/"},
		// Only the first slash separates; the API server rejects such a name, so no source matches.
		{value: "a/b/c", want: types.NamespacedName{Namespace: "a", Name: "b/c"}, wantOK: true},
	}
	for _, tc := range cases {
		t.Run(tc.value, func(t *testing.T) {
			got, ok := ParseSourceKey(tc.value)
			if ok != tc.wantOK || got != tc.want {
				t.Fatalf("got %v, %v; want %v, %v", got, ok, tc.want, tc.wantOK)
			}
		})
	}
}

func TestParseSourceKeyRoundTrip(t *testing.T) {
	src := source(nil)
	got, ok := ParseSourceKey(SourceKey(src))
	if !ok || got.Namespace != src.Namespace || got.Name != src.Name {
		t.Fatalf("got %v, %v for %q", got, ok, SourceKey(src))
	}
}
