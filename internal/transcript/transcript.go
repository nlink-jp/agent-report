// Package transcript reads gem-agent and lagent session transcripts.
//
// Both runtimes write the same JSONL shape: a "session" header record
// first, then records of the form {"ts", "kind", "data"}. This package
// only reads; it never writes, moves or deletes a transcript.
package transcript

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// SupportedSchema is the header schema this reader understands.
const SupportedSchema = 2

// Record is one transcript line. Data stays raw so each consumer decodes
// only the kinds it counts.
type Record struct {
	TS   time.Time       `json:"ts"`
	Kind string          `json:"kind"`
	Data json.RawMessage `json:"data"`
}

// Header is the first record's data.
type Header struct {
	Schema  int    `json:"schema"`
	Version string `json:"version"`
	Model   string `json:"model"`
}

// File is one transcript, read whole.
type File struct {
	Header    Header
	HeaderTS  time.Time
	HasHeader bool
	Records   []Record // every record after the header
	BadLines  int      // lines that were not a JSON record
}

// Discover returns every *.jsonl file under root, sorted. Symbolic links
// are not followed. A missing root is reported with fs.ErrNotExist.
func Discover(root string) ([]string, error) {
	if _, err := os.Stat(root); err != nil {
		return nil, err
	}
	var out []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			// An unreadable subdirectory is skipped, not fatal: the
			// report counts what it could read.
			if d != nil && d.IsDir() && path != root {
				return fs.SkipDir
			}
			return err
		}
		if d.Type().IsRegular() && strings.HasSuffix(d.Name(), ".jsonl") {
			out = append(out, path)
		}
		return nil
	})
	sort.Strings(out)
	return out, err
}

// ReadFile reads one transcript.
func ReadFile(path string) (File, error) {
	f, err := os.Open(path)
	if err != nil {
		return File{}, err
	}
	defer func() { _ = f.Close() }()
	return Read(f)
}

// Read parses a transcript stream. A line that is not a JSON record is
// counted, not fatal: a session still being written can end in a torn
// line. Lines may be very long (inline images), so no line limit applies.
func Read(r io.Reader) (File, error) {
	var out File
	br := bufio.NewReader(r)
	first := true
	for {
		line, err := br.ReadBytes('\n')
		if len(bytes.TrimSpace(line)) > 0 {
			var rec Record
			if jerr := json.Unmarshal(line, &rec); jerr != nil || rec.Kind == "" {
				out.BadLines++
			} else if first && rec.Kind == "session" {
				var h Header
				if json.Unmarshal(rec.Data, &h) == nil {
					out.Header, out.HeaderTS, out.HasHeader = h, rec.TS, true
				} else {
					out.BadLines++
				}
			} else {
				out.Records = append(out.Records, rec)
			}
			first = false
		}
		if errors.Is(err, io.EOF) {
			return out, nil
		}
		if err != nil {
			return out, err
		}
	}
}
