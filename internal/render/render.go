// Package render writes the report as JSON or as an indented table.
//
// The table is drawn by walking the JSON encoding itself, in field order,
// so the two cannot disagree: what a person checks on screen is exactly
// what the JSON hands over.
package render

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// JSON writes v as indented JSON.
func JSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// Table writes v as an indented key/value listing derived from its JSON.
func Table(w io.Writer, v any) error {
	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	t := &table{dec: dec}
	if err := t.value("", 0); err != nil {
		return err
	}
	_, err = io.WriteString(w, t.out.String())
	return err
}

type table struct {
	dec *json.Decoder
	out strings.Builder
}

func (t *table) line(depth int, s string) {
	t.out.WriteString(strings.Repeat("  ", depth))
	t.out.WriteString(s)
	t.out.WriteByte('\n')
}

// value consumes one JSON value. key is how it is labelled ("" for an
// array element); depth is its indentation.
func (t *table) value(key string, depth int) error {
	tok, err := t.dec.Token()
	if err != nil {
		return err
	}
	switch d := tok.(type) {
	case json.Delim:
		switch d {
		case '{':
			return t.object(key, depth)
		case '[':
			return t.array(key, depth)
		}
		return fmt.Errorf("unexpected %v", d)
	default:
		t.line(depth, label(key)+scalar(tok))
		return nil
	}
}

func (t *table) object(key string, depth int) error {
	if !t.dec.More() {
		_, err := t.dec.Token() // '}'
		t.line(depth, label(key)+"(none)")
		return err
	}
	inner := depth
	if key != "" {
		t.line(depth, key)
		inner = depth + 1
	}
	for t.dec.More() {
		k, err := t.dec.Token()
		if err != nil {
			return err
		}
		if err := t.value(k.(string), inner); err != nil {
			return err
		}
	}
	_, err := t.dec.Token() // '}'
	return err
}

func (t *table) array(key string, depth int) error {
	var scalars []string
	nested := false
	for t.dec.More() {
		tok, err := t.dec.Token()
		if err != nil {
			return err
		}
		if d, ok := tok.(json.Delim); ok && d == '{' {
			if !nested && len(scalars) == 0 {
				t.line(depth, key)
			}
			nested = true
			t.out.WriteByte('\n')
			if err := t.object("", depth+1); err != nil {
				return err
			}
			continue
		}
		scalars = append(scalars, scalar(tok))
	}
	if !nested {
		if len(scalars) == 0 {
			t.line(depth, label(key)+"(none)")
		} else {
			t.line(depth, label(key)+strings.Join(scalars, ", "))
		}
	}
	_, err := t.dec.Token() // ']'
	return err
}

func label(key string) string {
	if key == "" {
		return ""
	}
	return key + ": "
}

func scalar(tok json.Token) string {
	switch v := tok.(type) {
	case nil:
		// The report encodes an empty list or map as null.
		return "(none)"
	case string:
		if v == "" {
			return `""`
		}
		return v
	default:
		return fmt.Sprint(v)
	}
}
