package transcript

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const header = `{"ts":"2026-10-01T10:00:00+09:00","kind":"session","data":{"schema":2,"version":"v0.94.0","model":"gemini-3.8-flash","project":"/p"}}`

func TestReadHeaderRecordsAndBadLines(t *testing.T) {
	in := strings.Join([]string{
		header,
		``,
		`{"ts":"2026-10-01T10:00:01+09:00","kind":"message","data":{"role":"user","content":"hi"}}`,
		`not json`,
		`{"ts":"2026-10-01T10:00:02+09:00","data":{}}`, // no kind
		`{"ts":"2026-10-01T10:00:03+09:00","kind":"usage","data":{"prompt":5}}`,
		`{"ts":"2026-10-01T10:00:04+09:00","kind":"mess`, // torn last line
	}, "\n")
	f, err := Read(strings.NewReader(in))
	if err != nil {
		t.Fatal(err)
	}
	if !f.HasHeader || f.Header.Schema != 2 || f.Header.Version != "v0.94.0" || f.Header.Model != "gemini-3.8-flash" {
		t.Fatalf("header = %+v (has %v)", f.Header, f.HasHeader)
	}
	if len(f.Records) != 2 || f.Records[0].Kind != "message" || f.Records[1].Kind != "usage" {
		t.Fatalf("records = %+v", f.Records)
	}
	if f.BadLines != 3 {
		t.Fatalf("bad lines = %d; want 3", f.BadLines)
	}
}

func TestReadWithoutHeader(t *testing.T) {
	f, err := Read(strings.NewReader(`{"ts":"2026-10-01T10:00:01+09:00","kind":"message","data":{}}` + "\n" + header + "\n"))
	if err != nil {
		t.Fatal(err)
	}
	if f.HasHeader {
		t.Fatal("a session record that is not first was taken as the header")
	}
}

func TestReadVeryLongLine(t *testing.T) {
	long := strings.Repeat("x", 3<<20)
	in := header + "\n" + `{"ts":"2026-10-01T10:00:01+09:00","kind":"message","data":{"role":"tool","content":"` + long + `"}}` + "\n"
	f, err := Read(strings.NewReader(in))
	if err != nil {
		t.Fatal(err)
	}
	if len(f.Records) != 1 || f.BadLines != 0 {
		t.Fatalf("records %d, bad %d", len(f.Records), f.BadLines)
	}
}

func TestDiscover(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "a.jsonl"))
	mustWrite(t, filepath.Join(root, "projects", "-p", "b.jsonl"))
	mustWrite(t, filepath.Join(root, "projects", "-p", "notes.txt"))
	outside := t.TempDir()
	mustWrite(t, filepath.Join(outside, "c.jsonl"))
	if err := os.Symlink(outside, filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}

	got, err := Discover(root)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{filepath.Join(root, "a.jsonl"), filepath.Join(root, "projects", "-p", "b.jsonl")}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("Discover = %v; want %v (symlinks are not followed)", got, want)
	}

	if _, err := Discover(filepath.Join(root, "missing")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("missing root: err = %v; want fs.ErrNotExist", err)
	}
}

func mustWrite(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(header+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}
