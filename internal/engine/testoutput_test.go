package engine_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"testing"

	"github.com/smedijan/go-imp/internal/configuration"
	"github.com/smedijan/go-imp/internal/engine"
	"github.com/smedijan/go-imp/internal/gomodule"

	"github.com/smedijan/go-imp/internal/mutator"
)

func TestStreamingTestOutputPreservesClassification(t *testing.T) {
	fail := `{"Action":"fail","Package":"p","Test":"TestParent/child"}` + "\n" + `{"Action":"fail","Package":"p"}`
	huge := strings.Repeat("x", 2*1024*1024)
	for _, output := range []string{
		"", fail, fail + "garbage", fail + "{", fail + `{"Output":`, fail + `{"Test"`, fail + `{"Output":[`, fail + `{"Action":"fail","Package":"other"}`,
		`null ` + fail, `{"Action":null,"Test":false}`, `[]`,
		`{"action":"fail","package":"p","test":"T","Test":null}`,
		`{"Action":"pass","Action":"fail","Package":"p","Test":"T"}`,
		`{"Output":"` + huge + `"}` + fail,
		`{"Output":{"Test":"` + huge + `","Package":"` + huge + `"}}` + fail,
		`{"Test":"` + huge + `","Test":"","Action":"fail","Package":"p"}` + fail,
		`{"Test":"` + huge + `","Test":null,"Action":"fail","Package":"p"}`,
		`{"Test":"` + huge + `\q","Action":"fail","Package":"p"}`,
		`{"Test":"` + strings.Repeat(`\u0061`, 1024) + `","Action":"fail","Package":"p"}`,
		`{"Test":"` + strings.Repeat(`\u0061`, 1025) + `","Action":"fail","Package":"p"}`,
		`{"Output":"` + huge + `\q"}` + fail,
		`{"Output":` + strings.Repeat("1", 200000) + `}` + fail,
		`{"Output":1e999999}` + fail, `{"Output":01}` + fail,
		`{"Output":[` + strings.Repeat(`"small",`, 30000) + `null]}` + fail,
		`{"Output":{"Action":"x","nested":[true,false,null,1.25e-2]}}` + fail,
		`{"\u0041ction":"fail","Package":"p","Test":"T"}`,
		`{"Action":"fail","Package":"` + huge + `","Test":"T"}`,
		`{"Action":"fail","Package":"p","Test":"` + huge + `"}`,
		`{"Action":"` + huge + `","Package":"p","Test":"T"}`,
		`{"Action":"fail","Test":"T","Output":"unterminated`,
	} {
		for _, code := range []int{1, 2} {
			got, _ := observeTestOutput(t, output, code)
			want := legacyTestFailedStatus(code, output)
			if got != want {
				t.Fatalf("length=%d code=%d got %s want %s", len(output), code, got, want)
			}
		}
	}
}

func TestStreamingTestOutputBoundsEvidenceNotClassification(t *testing.T) {
	var input strings.Builder
	for i := 0; i < 100+10; i++ {
		fmt.Fprintf(&input, "{\"Action\":\"fail\",\"Package\":\"p\",\"Test\":\"TestParent/sub%d\"}\n", i)
	}
	input.WriteString(`{"Action":"fail","Package":"p"}`)
	status, output := observeTestOutput(t, input.String(), 1)
	if status != mutator.Killed || len(output.FailingTests) != 100 || !output.FailingTestsTruncated {
		t.Fatalf("incorrect bounded evidence: %+v", output)
	}
	input.WriteString(`{"Action":"fail","Package":"build-failed"}`)
	if status, _ := observeTestOutput(t, input.String(), 1); status != mutator.NotViable {
		t.Fatal("late package failure was lost")
	}
	input.WriteString("malformed")
	if _, output := observeTestOutput(t, input.String(), 1); output.JSONValid {
		t.Fatal("late invalid JSON was lost")
	}
}

// Generate payloads without constructing a proportional input string, so the
// allocation assertion measures the executor rather than fixture construction.
type repeatedOutputByte struct{}

func (repeatedOutputByte) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = 'x'
	}

	return len(p), nil
}

func TestStreamingTestOutputBoundsGeneratedPayloadAllocations(t *testing.T) {
	for _, tc := range []struct {
		name, prefix, suffix string
		truncated            bool
	}{
		{name: "nested Test", prefix: `{"Output":{"Test":"`, suffix: `"}}`},
		{name: "nested Package", prefix: `{"Output":{"Package":"`, suffix: `"}}`},
		{name: "root Test", prefix: `{"Action":"fail","Package":"p","Test":"`, suffix: `"}`, truncated: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			input := io.MultiReader(strings.NewReader(tc.prefix), io.LimitReader(repeatedOutputByte{}, 16*1024*1024), strings.NewReader(tc.suffix+`{"Action":"fail","Package":"p","Test":"TestReal"}{"Action":"fail","Package":"p"}`))
			runtime.GC()
			var before, after runtime.MemStats
			runtime.ReadMemStats(&before)
			status, evidence := observeTestOutputReader(t, input, 1)
			runtime.ReadMemStats(&after)
			if allocated := after.TotalAlloc - before.TotalAlloc; allocated > 8*1024*1024 {
				t.Fatalf("streaming allocated %d bytes for ignored/oversize metadata", allocated)
			}
			if status != mutator.Killed || evidence.FailingTestsTruncated != tc.truncated || len(evidence.FailingTests) != 1 || evidence.FailingTests[0].Test != "TestReal" {
				t.Fatalf("incorrect bounded evidence: status=%s evidence=%+v", status, evidence)
			}
		})
	}
}

func TestStreamingTestOutputRedactsEscapedEnvironmentValues(t *testing.T) {
	for _, secret := range []string{`quote"secret-value`, `backslash\secret-value`, "control\t\nsecret-value", "<html>&secret-value"} {
		t.Run(secret, func(t *testing.T) {
			t.Setenv("IMP_EVIDENCE_SECRET", secret)
			for _, html := range []bool{true, false} {
				var buffer bytes.Buffer
				encoder := json.NewEncoder(&buffer)
				encoder.SetEscapeHTML(html)
				if err := encoder.Encode(map[string]string{"Output": secret}); err != nil {
					t.Fatal(err)
				}
				_, evidence := observeTestOutput(t, buffer.String(), 1)
				var encoded bytes.Buffer
				encoder = json.NewEncoder(&encoded)
				encoder.SetEscapeHTML(html)
				if err := encoder.Encode(secret); err != nil {
					t.Fatal(err)
				}
				representation := strings.TrimSuffix(encoded.String(), "\n")
				representation = representation[1 : len(representation)-1]
				if !evidence.Stdout.Redacted || strings.Contains(evidence.Stdout.Text, representation) || strings.Contains(evidence.Stdout.Text, secret) {
					t.Fatalf("secret survived diagnostic redaction: %+v", evidence.Stdout)
				}
			}
		})
	}
}

func FuzzStreamingTestOutputClassification(f *testing.F) {
	for _, seed := range []string{`{"Action":"fail","Test":"T"}`, `{"Output":-0.2e+9}`, `{"Output":[{},null]}`, `null`, "", `{"Output":"\\u0041"}`} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, input string) {
		got, _ := observeTestOutput(t, input, 1)
		want := legacyTestFailedStatus(1, input)
		if got != want {
			t.Fatalf("classification mismatch got=%s want=%s input=%q", got, want, input)
		}
	})
}

func observeTestOutput(t *testing.T, output string, code int) (mutator.Status, *mutator.ExecutionEvidence) {
	t.Helper()

	return observeTestOutputReader(t, strings.NewReader(output), code)
}

func observeTestOutputReader(t *testing.T, output io.Reader, code int) (mutator.Status, *mutator.ExecutionEvidence) {
	t.Helper()
	viperSet(map[string]any{configuration.RunWorkersKey: 1})
	defer viperReset()
	// Race/coverage instrumentation slows generated streaming fixtures; their
	// deadline is independent of the allocation bound being tested.
	executor := engine.NewExecutor(gomodule.GoModule{CallingDir: "."}, newWdDealerStub(t), expectedTimeout, engine.WithExecContext(func(ctx context.Context, name string, args ...string) *exec.Cmd {
		cmd := helperCommand(ctx, "TestStreamingOutputProcess", name, args...)
		cmd.Env = append(cmd.Env, fmt.Sprintf("IMP_EVIDENCE_EXIT=%d", code))
		cmd.Stdin = output

		return cmd
	}))
	m := &evidenceMutant{mutantStub: &mutantStub{status: mutator.Runnable, pkg: "p"}}
	got := executeMutants(t, executor, m)[0]

	return got.Status(), m.ExecutionEvidence()
}

func TestStreamingOutputProcess(_ *testing.T) {
	if os.Getenv("GO_TEST_PROCESS") != "1" {
		return
	}
	_, _ = io.Copy(os.Stdout, os.Stdin)
	if os.Getenv("IMP_EVIDENCE_EXIT") == "2" {
		os.Exit(2)
	}
	os.Exit(1)
}

func legacyTestFailedStatus(exitCode int, output string) mutator.Status {
	if exitCode != 1 {
		return mutator.NotViable
	}

	type packageResult struct {
		packageFailed bool
		testFailed    bool
	}
	results := make(map[string]packageResult)
	decoder := json.NewDecoder(strings.NewReader(output))
	for {
		var event struct {
			Action  string
			Package string
			Test    string
		}
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

	testFailed := false
	for _, result := range results {
		if result.packageFailed && !result.testFailed {
			return mutator.NotViable
		}
		testFailed = testFailed || result.testFailed
	}
	if testFailed {
		return mutator.Killed
	}

	return mutator.NotViable
}
