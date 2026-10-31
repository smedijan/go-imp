package ledger

import (
	"crypto/sha256"
	"encoding/hex"
	"hash"
	"os"
	"sort"
	"strings"
)

const maxDiagnosticComponents = 131072

func componentDigest(components map[string]string, key string, values ...string) {
	if components == nil {
		return
	}
	h := sha256.New()
	for _, value := range values {
		frame(h, value)
	}
	componentCheckpoint(components, key, h)
}

func componentCheckpoint(components map[string]string, key string, h hash.Hash) {
	if components == nil {
		return
	}
	if len(components) >= maxDiagnosticComponents {
		components["diagnostic-limit-reached"] = hex.EncodeToString(sha256.New().Sum(nil))

		return
	}
	components[key] = hex.EncodeToString(h.Sum(nil))
}

func hashProcessEnvironment(h hash.Hash, components map[string]string) {
	environment := os.Environ()
	sort.Strings(environment)
	for _, value := range environment {
		frame(h, value)
		key := strings.SplitN(value, "=", 2)[0]
		if key == "" && strings.HasPrefix(value, "=") {
			key = strings.SplitN(value[1:], "=", 2)[0]
		}
		componentDigest(components, "process-env/"+key, value)
	}
	componentCheckpoint(components, "stage/process-env", h)
}
