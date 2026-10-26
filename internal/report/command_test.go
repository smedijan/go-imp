package report_test

import (
	"bytes"
	"os/exec"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/smedijan/go-imp/internal/mutator"
	"github.com/smedijan/go-imp/internal/report"
)

func TestCommandArgumentsRoundTrip(t *testing.T) {
	for _, tc := range []struct {
		name     string
		argument string
		quoted   string
	}{
		{name: "ordinary", argument: "integration", quoted: "integration"},
		{name: "space", argument: "tag1 tag2", quoted: "'tag1 tag2'"},
		{name: "empty", argument: "", quoted: "''"},
		{name: "variable", argument: "$IMP_QUOTE_TEST", quoted: "'$IMP_QUOTE_TEST'"},
		{name: "substitution", argument: "$(printf substituted)", quoted: "'$(printf substituted)'"},
		{name: "backticks", argument: "`printf substituted`", quoted: "'`printf substituted`'"},
		{name: "single quote", argument: "a'b", quoted: `'a'"'"'b'`},
		{name: "double quote", argument: `a"b`, quoted: `'a"b'`},
		{name: "backslash", argument: `a\b`, quoted: `'a\b'`},
		{name: "semicolon", argument: "a;b", quoted: "'a;b'"},
		{name: "glob", argument: "*.go", quoted: "'*.go'"},
		{name: "newline", argument: "a\nb", quoted: "'a\nb'"},
		{name: "Unicode", argument: "café", quoted: "'café'"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("IMP_QUOTE_TEST", "expanded")
			args := []string{"go", "test", "-tags", tc.argument}
			doc, _ := renderJSON(t, report.Results{Mutants: []mutator.Mutator{stubMutant{status: mutator.Lived, command: args}}}, report.Options{})
			command := doc.Groups["LIVED"][""][0].TestCommand
			if want := "go test -tags " + tc.quoted; command != want {
				t.Errorf("command=%q, want %q", command, want)
			}
			if _, err := exec.LookPath("sh"); err != nil {
				t.Skip("POSIX shell unavailable; explicit quoting assertions still ran")
			}
			//nolint:gosec // exercise serialization with fixed, test-owned shell arguments
			output, err := exec.Command("sh", "-c", "printf '%s\\0' "+command).Output()
			if err != nil {
				t.Fatal(err)
			}
			got := strings.Split(string(bytes.TrimSuffix(output, []byte{0})), "\x00")
			if diff := cmp.Diff(args, got); diff != "" {
				t.Errorf("shell argument round-trip (-want +got):\n%s", diff)
			}
		})
	}
}
