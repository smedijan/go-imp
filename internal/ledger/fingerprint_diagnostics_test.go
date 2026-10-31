package ledger_test

import (
	"context"
	"encoding/hex"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/smedijan/go-imp/internal/ledger"
)

func captureFingerprint(t *testing.T, root, path, settings, tags string) (string, map[string]string) {
	t.Helper()
	digest, components, err := ledger.FingerprintDiagnostics(context.Background(), root, path, settings, tags)
	if err != nil {
		t.Fatal(err)
	}

	return digest, components
}

func reportFingerprintChanges(t *testing.T, before, after map[string]string) {
	t.Helper()
	keys := make(map[string]bool)
	for key := range before {
		keys[key] = true
	}
	for key := range after {
		keys[key] = true
	}
	changed := make([]string, 0)
	for key := range keys {
		if before[key] != after[key] {
			changed = append(changed, key)
		}
	}
	sort.Strings(changed)
	t.Logf("fingerprint changed components=%d truncated=%t", len(changed), after["diagnostic-limit-reached"] != "")
	for _, key := range changed[:min(32, len(changed))] {
		t.Logf("component %q digest %s -> %s", key, before[key], after[key])
	}
}

func TestFingerprintDiagnosticsPreserveDigestAndHashSecrets(t *testing.T) {
	t.Setenv("CGO_ENABLED", "0")
	t.Setenv("GOWORK", "off")
	secret := "fingerprint-diagnostic-secret-never-log"
	t.Setenv("IMP_DIAGNOSTIC_SECRET", secret)
	root, _ := fixture(t)
	path := t.TempDir() + "/ledger"
	original, firstComponents := captureFingerprint(t, root, path, "settings", "")
	diagnostic, components := captureFingerprint(t, root, path, "settings", "")
	if original != diagnostic {
		reportFingerprintChanges(t, firstComponents, components)
		t.Fatal("unstable diagnostic fingerprint")
	}
	normal, err := ledger.Fingerprint(context.Background(), root, path, "settings", "")
	if err != nil {
		t.Fatal(err)
	}
	if normal != diagnostic {
		t.Fatal("diagnostic tracing changed the fingerprint")
	}
	if components["process-env/IMP_DIAGNOSTIC_SECRET"] == "" {
		t.Fatal("missing hashed environment component")
	}
	for key, value := range components {
		if strings.Contains(key, secret) || strings.Contains(key, root) {
			t.Fatal("diagnostic key exposes raw input")
		}
		if len(value) != 64 {
			t.Fatal("diagnostic value is not a SHA-256 digest")
		}
		if _, err = hex.DecodeString(value); err != nil {
			t.Fatal("diagnostic value is not hexadecimal")
		}
	}
}

func TestFingerprintDiagnosticsIdentifyTimestampOnlyInputChange(t *testing.T) {
	t.Setenv("CGO_ENABLED", "0")
	t.Setenv("GOWORK", "off")
	root, _ := fixture(t)
	path := filepath.Join(t.TempDir(), "ledger")
	beforeDigest, before := captureFingerprint(t, root, path, "", "")
	input := filepath.Join(root, "data.txt")
	info, err := os.Stat(input)
	if err != nil {
		t.Fatal(err)
	}
	changed := info.ModTime().Add(-time.Hour)
	if err = os.Chtimes(input, changed, changed); err != nil {
		t.Fatal(err)
	}
	afterDigest, after := captureFingerprint(t, root, path, "", "")
	if beforeDigest == afterDigest {
		t.Fatal("timestamp input change not fingerprinted")
	}
	found := false
	for key, value := range before {
		if strings.HasSuffix(key, "/entry/data.txt/mtime") {
			found = true
			if value == after[key] {
				t.Fatal("changed timestamp component not identified")
			}
			contentKey := strings.TrimSuffix(key, "mtime") + "content"
			if before[contentKey] != after[contentKey] {
				t.Fatal("diagnostics mislabeled timestamp-only change as content change")
			}
		}
		if strings.HasSuffix(key, "/selected") || strings.HasSuffix(key, "/canonical") {
			if value != after[key] {
				t.Fatal("unchanged root identity mislabeled")
			}
		}
	}
	if !found {
		t.Fatal("missing timestamp diagnostic component")
	}
}
