package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
)

// SchemaVersion is carried by every NDJSON record so a reader can branch on it.
const SchemaVersion = 1

// emitter writes results to stdout: one NDJSON object per line under --json,
// otherwise one tab-separated line. Both forms are line-buffered and safe for
// concurrent writers, so `| head` and `| grep` behave.
type emitter struct {
	mu  sync.Mutex
	w   io.Writer
	enc *json.Encoder
}

func newEmitter(w io.Writer) *emitter {
	e := &emitter{w: w}
	if global.json {
		e.enc = json.NewEncoder(w)
	}
	return e
}

// record writes obj as NDJSON, or line as text. line must not contain a newline.
func (e *emitter) record(obj map[string]any, line string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.enc != nil {
		obj["v"] = SchemaVersion
		return e.enc.Encode(obj)
	}
	_, err := fmt.Fprintln(e.w, line)
	return err
}

// progress goes to stderr, never stdout, so it can never corrupt piped output.
// It is dropped entirely under --quiet, and under --json it is emitted as a
// record on stdout instead (an agent reading NDJSON wants it in-stream).
func (e *emitter) progress(obj map[string]any, line string) {
	if global.quiet {
		return
	}
	if e.enc != nil {
		_ = e.record(obj, "")
		return
	}
	fmt.Fprintln(os.Stderr, line)
}

// summary is the last thing a command writes: a machine record under --json,
// a human sentence on stderr otherwise.
func (e *emitter) summary(obj map[string]any, line string) {
	if e.enc != nil {
		_ = e.record(obj, "")
		return
	}
	if !global.quiet {
		fmt.Fprintln(os.Stderr, line)
	}
}

// tabs joins fields into one record line, guarding the separator contract.
func tabs(fields ...string) string {
	for i, f := range fields {
		fields[i] = strings.NewReplacer("\t", " ", "\n", " ", "\r", " ").Replace(f)
	}
	return strings.Join(fields, "\t")
}
