package cmd_test

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestPrePushMutationFileSelection(t *testing.T) {
	hk, err := exec.LookPath("hk")
	if err != nil {
		t.Skip("hk is optional for Go-only environments; installed by mise for local hooks")
	}
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name     string
		files    []string
		included bool
	}{
		{name: "documentation only", files: []string{"README.md"}},
		{name: "Go source", files: []string{"internal/report/report.go"}, included: true},
		{name: "Go tests", files: []string{"internal/report/report_test.go"}, included: true},
		{name: "mixed", files: []string{"README.md", "cmd/run.go"}, included: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fileList := filepath.Join(t.TempDir(), "selected-files")
			writeFixture(t, fileList, strings.Join(tc.files, "\x00")+"\x00")
			// Plan only: no checks, Git hooks, mutations or file changes are executed.
			//nolint:gosec // hk is resolved by LookPath and arguments are test-owned
			command := exec.Command(hk, "run", "pre-push", "--files0-from", fileList, "--plan", "--json")
			command.Dir = root
			var stdout, stderr bytes.Buffer
			command.Stdout, command.Stderr = &stdout, &stderr
			if err := command.Run(); err != nil {
				t.Fatalf("hook plan: %v\n%s", err, &stderr)
			}
			var plan struct {
				Steps []struct {
					Name   string `json:"name"`
					Status string `json:"status"`
				} `json:"steps"`
			}
			if err := json.Unmarshal(stdout.Bytes(), &plan); err != nil {
				t.Fatal(err)
			}
			found := false
			for _, step := range plan.Steps {
				if step.Name == "mutation-test" {
					found = true
					if got := step.Status == "included"; got != tc.included {
						t.Errorf("mutation-test status=%s for %v", step.Status, tc.files)
					}
				}
			}
			if !found {
				t.Fatal("pre-push has no mutation-test step")
			}
		})
	}
}
