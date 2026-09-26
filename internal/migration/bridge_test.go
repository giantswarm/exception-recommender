package migration

import (
	"testing"

	"github.com/stretchr/testify/assert"
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

			assert.Equal(t, tc.wantOK, ok)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestParseSourceKeyRoundTrip(t *testing.T) {
	// arrange
	src := source(nil)

	// act
	got, ok := ParseSourceKey(SourceKey(src))

	// assert
	assert.True(t, ok)
	assert.Equal(t, types.NamespacedName{Namespace: src.Namespace, Name: src.Name}, got)
}
