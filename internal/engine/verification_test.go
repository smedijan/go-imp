package engine //nolint:testpackage // Verify the exact environment passed to snapshot-only subprocesses.

import (
	"strings"
	"testing"
)

func TestVerificationEnvironmentStripsAllGitStateAndWorkspace(t *testing.T) {
	for key, value := range map[string]string{
		"GIT_DIR": "victim.git", "GIT_WORK_TREE": "victim", "GIT_INDEX_FILE": "victim.index",
		"GIT_CONFIG_COUNT": "1", "GIT_CONFIG_KEY_0": "core.worktree", "GIT_CONFIG_VALUE_0": "victim",
		"GIT_UNRECOGNIZED_HOOK_STATE": "must also be removed", "GOWORK": "external/go.work", "IMP_VERIFY_RETAIN": "retained",
	} {
		t.Setenv(key, value)
	}
	env := verificationEnvironment()
	retained, workspace := false, 0
	for _, entry := range env {
		if strings.HasPrefix(entry, "GIT_") {
			t.Fatalf("inherited Git state retained: %q", entry)
		}
		if strings.HasPrefix(entry, "GOWORK=") {
			workspace++
			if entry != "GOWORK=off" {
				t.Fatalf("external workspace retained: %q", entry)
			}
		}
		if entry == "IMP_VERIFY_RETAIN=retained" {
			retained = true
		}
	}
	if !retained || workspace != 1 {
		t.Fatalf("unexpected environment retained=%t workspace entries=%d", retained, workspace)
	}
}
