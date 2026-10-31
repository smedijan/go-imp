package engine //nolint:testpackage // Exercise streaming grammar and evidence limits without subprocesses.

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/smedijan/go-imp/internal/mutator"
)

// Keep the pre-streaming classifier as the differential oracle: diagnostic
// retention and numeric payload reduction must not change KILLED eligibility.
func legacyOutputStatus(exitCode int, output string) mutator.Status {
	if exitCode != 1 {
		return mutator.NotViable
	}
	type packageResult struct{ packageFailed, testFailed bool }
	results := map[string]packageResult{}
	decoder := json.NewDecoder(strings.NewReader(output))
	for {
		var event struct{ Action, Package, Test string }
		if err := decoder.Decode(&event); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}

			return mutator.NotViable
		}
		result := results[event.Package]
		if event.Action == "fail" {
			if event.Test == "" {
				result.packageFailed = true
			} else {
				result.testFailed = true
			}
		}
		results[event.Package] = result
	}
	failed := false
	for _, result := range results {
		if result.packageFailed && !result.testFailed {
			return mutator.NotViable
		}
		failed = failed || result.testFailed
	}
	if failed {
		return mutator.Killed
	}

	return mutator.NotViable
}

func assertLegacyOutputStatus(t *testing.T, output string) {
	t.Helper()
	got := collectTestOutput(strings.NewReader(output))
	for _, exitCode := range []int{1, 2} {
		if status, want := got.status(exitCode), legacyOutputStatus(exitCode, output); status != want {
			t.Fatalf("stream classification=%s legacy=%s exit=%d output=%q", status, want, exitCode, output)
		}
	}
}

func TestTestOutputNumericGrammarDifferential(t *testing.T) {
	// Exhaustive short tokens include invalid transitions followed by a digit,
	// not just incomplete tokens which would fail at the delimiter either way.
	const alphabet = "01-+.e"
	var check func(string, int)
	check = func(number string, remaining int) {
		output := `{"Output":` + number + `,"Action":"fail","Package":"p","Test":"T"}`
		assertLegacyOutputStatus(t, output)
		if remaining == 0 {
			return
		}
		for _, digit := range alphabet {
			check(number+string(digit), remaining-1)
		}
	}
	check("", 5)
	for _, number := range []string{"9", "-9", "9-1", "9+1", "9.9", "-0.99", "0E0", "1E+9", "-1.9e-9", "1234567890", "1e999999", "0e-999999", "1e+", "1e-", "-0.0E-1", "0.1.0", "1E.1", "1e1e1"} {
		assertLegacyOutputStatus(t, `{"Output":`+number+`,"Action":"fail","Package":"p","Test":"T"}`)
	}
}

func TestTestOutputUnicodeEscapeGrammarDifferential(t *testing.T) {
	for position := 0; position < 4; position++ {
		for digit := byte(0); digit < 128; digit++ {
			escape := []byte("0000")
			escape[position] = digit
			assertLegacyOutputStatus(t, `{"Output":"\u`+string(escape)+`","Action":"fail","Package":"p","Test":"T"}`)
		}
	}
}

func TestTestOutputMaximumNestingMatchesLegacy(t *testing.T) {
	for _, depth := range []int{9999, 10000} {
		output := `{"Output":` + strings.Repeat("[", depth) + "0" + strings.Repeat("]", depth) + `,"Action":"fail","Package":"p","Test":"T"}`
		assertLegacyOutputStatus(t, output)
	}
}

func TestTestOutputFailingNameBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name, pkg, test, encodedTest string
		truncated                    bool
	}{
		{name: "package limit", pkg: strings.Repeat("p", 1024), test: "T"},
		{name: "test limit", pkg: "p", test: strings.Repeat("t", 1024)},
		{name: "escaped test limit", pkg: "p", test: strings.Repeat("a", 1024), encodedTest: `"` + strings.Repeat(`\u0061`, 1024) + `"`},
		{name: "package over limit", pkg: strings.Repeat("p", 1025), test: "T", truncated: true},
		{name: "test over limit", pkg: "p", test: strings.Repeat("t", 1025), truncated: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pkg, _ := json.Marshal(tc.pkg)
			test, _ := json.Marshal(tc.test)
			if tc.encodedTest != "" {
				test = []byte(tc.encodedTest)
			}
			output := `{"Action":"fail","Package":` + string(pkg) + `,"Test":` + string(test) + `}`
			result := collectTestOutput(strings.NewReader(output))
			assertLegacyOutputStatus(t, output)
			if result.truncated != tc.truncated || result.status(1) != mutator.Killed {
				t.Fatalf("incorrect classification/truncation: %+v", result)
			}
			if tc.truncated {
				if len(result.failing) != 0 {
					t.Fatal("oversized name was attributed")
				}
			} else if len(result.failing) != 1 || result.failing[0].Package != tc.pkg || result.failing[0].Test != tc.test {
				t.Fatalf("exact-limit attribution lost: %+v", result.failing)
			}
		})
	}
}

func TestTestOutputFailingEventCountBoundaries(t *testing.T) {
	for _, count := range []int{99, 100, 101} {
		var output strings.Builder
		for i := 0; i < count; i++ {
			fmt.Fprintf(&output, "{\"Action\":\"fail\",\"Package\":\"p\",\"Test\":\"T%d\"}\n", i)
		}
		result := collectTestOutput(strings.NewReader(output.String()))
		if len(result.failing) != min(count, 100) || result.truncated != (count > 100) || result.status(1) != mutator.Killed {
			t.Fatalf("count=%d result=%+v", count, result)
		}
		if last := result.failing[len(result.failing)-1].Test; last != fmt.Sprintf("T%d", min(count, 100)-1) {
			t.Fatalf("retention is not a prefix: %q", last)
		}
	}
}

func TestTestOutputRestoresRootScopeAfterNestedPayload(t *testing.T) {
	output := `{"Output":[{"Package":"ignored","Test":"ignored"}],"Action":"fail","Package":"p","Test":"` + strings.Repeat("t", 7000) + `"}`
	result := collectTestOutput(strings.NewReader(output))
	assertLegacyOutputStatus(t, output)
	if !result.valid || result.status(1) != mutator.Killed || !result.truncated || len(result.failing) != 0 {
		t.Fatalf("nested payload changed root classification: %+v", result)
	}
}

func TestSynchronizedBufferDiagnosticBoundaries(t *testing.T) {
	const limit = 16384
	for _, size := range []int{0, limit - 1, limit, limit + 1} {
		var buffer synchronizedBuffer
		input := strings.Repeat("x", size)
		// Include empty writes and split exactly at the limit across writes.
		for _, part := range []string{"", input[:min(size, limit)], input[min(size, limit):]} {
			if n, err := buffer.Write([]byte(part)); err != nil || n != len(part) {
				t.Fatalf("write=%d,%v", n, err)
			}
		}
		diagnostic := buffer.diagnostic()
		if buffer.String() != input[:min(size, limit)] || diagnostic.Truncated != (size > limit) {
			t.Fatalf("size=%d retained=%d truncated=%t", size, len(buffer.String()), diagnostic.Truncated)
		}
	}
}

func TestSynchronizedBufferRedactsEscapesAndLongestValues(t *testing.T) {
	// #nosec G101 -- Synthetic environment-redaction fixture, not a credential.
	const secret = "imp-evidence-<quote\"and\\slash>\t\n-secret"
	t.Setenv("IMP_CONTRACT_LONG_SECRET", secret)
	t.Setenv("IMP_CONTRACT_SHORT_SECRET", "imp-evidence-")
	for _, html := range []bool{true, false} {
		var encoded bytes.Buffer
		encoder := json.NewEncoder(&encoded)
		encoder.SetEscapeHTML(html)
		if err := encoder.Encode(secret); err != nil {
			t.Fatal(err)
		}
		var buffer synchronizedBuffer
		if _, err := buffer.Write(encoded.Bytes()); err != nil {
			t.Fatal(err)
		}
		diagnostic := buffer.diagnostic()
		if !diagnostic.Redacted || diagnostic.Truncated || strings.Contains(diagnostic.Text, "secret") || strings.Contains(diagnostic.Text, "quote") || strings.Contains(diagnostic.Text, "slash") {
			t.Fatalf("escaped secret suffix survived: %+v", diagnostic)
		}
		if diagnostic.RedactionPolicy != "environment-values-removed; other-secrets-not-detected" {
			t.Fatalf("unexpected policy: %q", diagnostic.RedactionPolicy)
		}
	}
}
