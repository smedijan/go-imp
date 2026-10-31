package engine //nolint:testpackage // Verify bounded source labels directly, without discovery/process overhead.

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestObservedNameExactByteAndRuneBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name, value, want string
		known, truncated  bool
	}{
		{name: "unknown"},
		{name: "below limit", value: strings.Repeat("x", 1023), want: strings.Repeat("x", 1023), known: true},
		{name: "exact limit", value: strings.Repeat("x", 1024), want: strings.Repeat("x", 1024), known: true},
		{name: "over limit", value: strings.Repeat("x", 1025), want: strings.Repeat("x", 1024), known: true, truncated: true},
		{name: "exact multibyte limit", value: strings.Repeat("é", 512), want: strings.Repeat("é", 512), known: true},
		{name: "crossing rune", value: strings.Repeat("x", 1023) + "界", want: strings.Repeat("x", 1023), known: true, truncated: true},
		{name: "rune ending at limit", value: strings.Repeat("x", 1021) + "界tail", want: strings.Repeat("x", 1021) + "界", known: true, truncated: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := observedName(tc.value)
			if got.Value != tc.want || got.Known != tc.known || got.Truncated != tc.truncated || !utf8.ValidString(got.Value) {
				t.Fatalf("unexpected bounded label: %+v", got)
			}
		})
	}
}
