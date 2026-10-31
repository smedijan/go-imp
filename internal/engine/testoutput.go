package engine

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"sort"
	"strings"
	"sync"

	"github.com/smedijan/go-imp/internal/mutator"
)

const diagnosticLimit = 16 * 1024
const failingTestLimit = 100
const failingNameLimit = 1024

// synchronizedBuffer retains only a prefix; classification never uses it.
type synchronizedBuffer struct {
	mu        sync.Mutex
	buf       []byte
	truncated bool
}

func (b *synchronizedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	n := min(len(p), diagnosticLimit-len(b.buf))
	b.buf = append(b.buf, p[:n]...)
	b.truncated = b.truncated || n < len(p)

	return len(p), nil
}

func (b *synchronizedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()

	return string(b.buf)
}

func (b *synchronizedBuffer) diagnostic() mutator.Diagnostic {
	text := b.String()
	var values []string
	for _, entry := range os.Environ() {
		_, value, _ := strings.Cut(entry, "=")
		if value != "" {
			values = append(values, value)
			encoded, _ := json.Marshal(value)
			values = append(values, string(encoded[1:len(encoded)-1]))
			var buffer bytes.Buffer
			encoder := json.NewEncoder(&buffer)
			encoder.SetEscapeHTML(false)
			_ = encoder.Encode(value)
			plainJSON := bytes.TrimSuffix(buffer.Bytes(), []byte{'\n'})
			values = append(values, string(plainJSON[1:len(plainJSON)-1]))
		}
	}
	sort.Slice(values, func(i, j int) bool { return len(values[i]) > len(values[j]) })
	redacted := false
	for _, value := range values {
		if strings.Contains(text, value) {
			// Empty replacement cannot grow the retained prefix.
			text = strings.ReplaceAll(text, value, "")
			redacted = true
		}
	}

	return mutator.Diagnostic{Text: text, Truncated: b.truncated, Redacted: redacted,
		RedactionPolicy: "environment-values-removed; other-secrets-not-detected"}
}

type testOutput struct {
	results          map[string]packageFailure
	valid            bool
	failing          []mutator.FailingTest
	passing          []mutator.FailingTest
	selectedPassing  *mutator.FailingTest
	passingTruncated bool
	truncated        bool
}

type packageFailure struct{ packageFailed, testFailed bool }

func collectTestOutput(reader io.Reader) testOutput {
	return collectSelectedTestOutput(reader, "")
}

func collectSelectedTestOutput(reader io.Reader, selectedTest string) testOutput {
	result := testOutput{results: make(map[string]packageFailure), valid: true, failing: []mutator.FailingTest{}}
	// The reducer validates and discards ignored string/number payloads before
	// encoding/json buffers a token. Relevant metadata retains exact semantics.
	decoder := json.NewDecoder(&jsonPayloadReducer{reader: bufio.NewReader(reader)})
	for {
		event, err := readTestEvent(decoder)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			result.valid = false
			// A malformed stream must not block the child process's remaining output.
			_, _ = io.Copy(io.Discard, reader)

			break
		}
		if event.Action == "pass" && event.Test != "" {
			withinNameBounds := len(event.Test) <= failingNameLimit && len(event.Package) <= failingNameLimit
			if withinNameBounds && len(result.passing) < failingTestLimit {
				result.passing = append(result.passing, mutator.FailingTest{Package: event.Package, Test: event.Test})
			} else {
				result.passingTruncated = true
			}
			// Subtests complete before their parent. Retain the selected parent
			// independently so a long passing subtest prefix cannot hide it.
			if withinNameBounds && event.Test == selectedTest {
				result.selectedPassing = &mutator.FailingTest{Package: event.Package, Test: event.Test}
			}
		}
		state := result.results[event.Package]
		if event.Action == "fail" {
			if event.Test == "" {
				state.packageFailed = true
			} else {
				state.testFailed = true
				if len(result.failing) < failingTestLimit && len(event.Package) <= failingNameLimit && len(event.Test) <= failingNameLimit {
					result.failing = append(result.failing, mutator.FailingTest{Package: event.Package, Test: event.Test})
				} else {
					result.truncated = true
				}
			}
		}
		result.results[event.Package] = state
	}

	return result
}

type testEvent struct{ Action, Package, Test string }

// Walk one event token-by-token so repeated ignored fields and nested payloads
// cannot make the decoder retain a whole, arbitrarily large event.
func readTestEvent(decoder *json.Decoder) (testEvent, error) {
	var event testEvent
	token, err := decoder.Token()
	if err != nil {
		return event, err
	}

	if token == nil {
		return event, nil
	}
	if token != json.Delim('{') {
		return event, errInvalidJSONPayload
	}
	for decoder.More() {
		key, err := decoder.Token()
		if err != nil {
			return event, unexpectedEventEOF(err)
		}
		name, ok := key.(string)
		if !ok {
			return event, errInvalidJSONPayload
		}
		var field *string
		switch {
		case strings.EqualFold(name, "Action"):
			field = &event.Action
		case strings.EqualFold(name, "Package"):
			field = &event.Package
		case strings.EqualFold(name, "Test"):
			field = &event.Test
		}
		value, err := decoder.Token()
		if err != nil {
			return event, unexpectedEventEOF(err)
		}
		if field != nil {
			if value == nil {
				continue
			}
			text, ok := value.(string)
			if !ok {
				return event, errInvalidJSONPayload
			}
			*field = text
		} else if delim, ok := value.(json.Delim); ok {
			if delim != '{' && delim != '[' {
				return event, errInvalidJSONPayload
			}
			depth := 1
			for depth > 0 {
				nested, err := decoder.Token()
				if err != nil {
					return event, unexpectedEventEOF(err)
				}
				if d, ok := nested.(json.Delim); ok {
					switch d {
					case '{', '[':
						depth++
					case '}', ']':
						depth--
					}
					if depth >= 10000 {
						return event, errInvalidJSONPayload
					}
				}
			}
		}
	}
	_, err = decoder.Token()

	return event, unexpectedEventEOF(err)
}

// EOF is clean only before an event starts, never inside an object.
func unexpectedEventEOF(err error) error {
	if errors.Is(err, io.EOF) {
		return io.ErrUnexpectedEOF
	}

	return err
}

func (o testOutput) status(exitCode int) mutator.Status {
	if exitCode != 1 || !o.valid {
		return mutator.NotViable
	}
	failed := false
	for _, result := range o.results {
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

// jsonPayloadReducer validates ignored strings while bounding their storage.
// Only root Package strings retain exact identity. Oversize root Test values
// become a nonempty string longer than the evidence limit: classification only
// needs emptiness, and collection must never publish that synthetic name.
type jsonPayloadReducer struct {
	reader     *bufio.Reader
	pending    []byte
	lastString string
	preserve   bool
	testName   bool
	depth      int
}

func (r *jsonPayloadReducer) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if len(r.pending) == 0 {
		c, err := r.reader.ReadByte()
		if err != nil {
			return 0, err
		}
		switch {
		case c == '"':
			token, err := r.stringToken(r.preserve, r.testName)
			if err != nil {
				return 0, err
			}
			r.preserve = false
			r.testName = false
			r.lastString = ""
			if len(token) <= 256 {
				_ = json.Unmarshal(token, &r.lastString)
			}
			r.pending = token
		case c == ':':
			r.preserve = r.depth == 1 && strings.EqualFold(r.lastString, "Package")
			r.testName = r.depth == 1 && strings.EqualFold(r.lastString, "Test")
			r.pending = []byte{c}
		case c == '-' || c >= '0' && c <= '9':
			if err := r.numberToken(c); err != nil {
				return 0, err
			}
			r.preserve = false
			r.testName = false
			r.pending = []byte{'0'}
		default:
			switch c {
			case '{', '[':
				r.depth++
			case '}', ']':
				r.depth--
			}
			if c != ' ' && c != '\n' && c != '\r' && c != '\t' {
				r.preserve = false
				r.testName = false
			}
			r.pending = []byte{c}
		}
	}
	n := copy(p, r.pending)
	r.pending = r.pending[n:]

	return n, nil
}

var errInvalidJSONPayload = errors.New("invalid JSON payload")

func (r *jsonPayloadReducer) stringToken(preserve, testName bool) ([]byte, error) {
	token := []byte{'"'}
	limit := 256
	if testName {
		limit = 6*failingNameLimit + 2
	}
	shortened := false
	for {
		c, err := r.reader.ReadByte()
		if err != nil {
			return nil, io.ErrUnexpectedEOF
		}
		if !shortened {
			token = append(token, c)
		}
		if c == '"' {
			break
		}
		if c < 0x20 {
			return nil, errInvalidJSONPayload
		}
		if c == '\\' {
			c, err = r.reader.ReadByte()
			if err != nil {
				return nil, io.ErrUnexpectedEOF
			}
			if !shortened {
				token = append(token, c)
			}
			switch c {
			case '"', '\\', '/', 'b', 'f', 'n', 'r', 't':
			case 'u':
				for range 4 {
					h, readErr := r.reader.ReadByte()
					if readErr != nil {
						return nil, io.ErrUnexpectedEOF
					}
					if (h < '0' || h > '9') && (h < 'a' || h > 'f') && (h < 'A' || h > 'F') {
						return nil, errInvalidJSONPayload
					}
					if !shortened {
						token = append(token, h)
					}
				}
			default:
				return nil, errInvalidJSONPayload
			}
		}
		if !preserve && len(token) > limit {
			token = nil
			shortened = true
		}
	}
	if shortened {
		if testName {
			return []byte(`"` + strings.Repeat("x", failingNameLimit+1) + `"`), nil
		}

		return []byte(`""`), nil
	}

	return token, nil
}

// Validate JSON's numeric grammar without retaining arbitrary-length digits.
func (r *jsonPayloadReducer) numberToken(first byte) error {
	// Rows: start, sign, zero, integer, dot, fraction, exponent, exponent
	// sign, exponent digits. Columns: zero, other digit, minus, plus, dot, e/E.
	transitions := [9][6]int{
		{2, 3, 1, -1, -1, -1},
		{2, 3, -1, -1, -1, -1},
		{-1, -1, -1, -1, 4, 6},
		{3, 3, -1, -1, 4, 6},
		{5, 5, -1, -1, -1, -1},
		{5, 5, -1, -1, -1, 6},
		{8, 8, 7, 7, -1, -1},
		{8, 8, -1, -1, -1, -1},
		{8, 8, -1, -1, -1, -1},
	}
	state := transitions[0][numericClass(first)]
	for {
		c, err := r.reader.ReadByte()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}
		class := numericClass(c)
		if class < 0 {
			_ = r.reader.UnreadByte()

			break
		}
		state = transitions[state][class]
		if state < 0 {
			return errInvalidJSONPayload
		}
	}
	if state == 1 || state == 4 || state == 6 || state == 7 {
		return errInvalidJSONPayload
	}

	return nil
}

func numericClass(c byte) int {
	switch {
	case c == '0':
		return 0
	case c >= '1' && c <= '9':
		return 1
	case c == '-':
		return 2
	case c == '+':
		return 3
	case c == '.':
		return 4
	case c == 'e' || c == 'E':
		return 5
	default:
		return -1
	}
}
