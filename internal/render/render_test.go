package render

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

type inner struct {
	Count int            `json:"count"`
	ByKey map[string]int `json:"by_key"`
}

type sample struct {
	Name   string         `json:"name"`
	Ratio  float64        `json:"ratio"`
	Empty  string         `json:"empty"`
	Inner  inner          `json:"inner"`
	None   map[string]int `json:"none"`
	Tags   []string       `json:"tags"`
	NoTags []string       `json:"no_tags"`
	Items  []inner        `json:"items"`
}

var v = sample{
	Name:  "gem-agent",
	Ratio: 0.851,
	Inner: inner{Count: 3, ByKey: map[string]int{"b": 2, "a": 1}},
	None:  map[string]int{},
	Tags:  []string{"launch_mode", "x"},
	Items: []inner{{Count: 7, ByKey: map[string]int{"z": 9}}},
}

func TestTableShowsEveryLeafOfTheJSON(t *testing.T) {
	var js, tbl bytes.Buffer
	if err := JSON(&js, v); err != nil {
		t.Fatal(err)
	}
	if err := Table(&tbl, v); err != nil {
		t.Fatal(err)
	}
	var decoded any
	if err := json.Unmarshal(js.Bytes(), &decoded); err != nil {
		t.Fatal(err)
	}
	for _, leaf := range leaves("", decoded) {
		if !strings.Contains(tbl.String(), leaf) {
			t.Errorf("table lacks %q:\n%s", leaf, tbl.String())
		}
	}
}

func TestTableLayout(t *testing.T) {
	var tbl bytes.Buffer
	if err := Table(&tbl, v); err != nil {
		t.Fatal(err)
	}
	want := `name: gem-agent
ratio: 0.851
empty: ""
inner
  count: 3
  by_key
    a: 1
    b: 2
none: (none)
tags: launch_mode, x
no_tags: (none)
items

  count: 7
  by_key
    z: 9
`
	if tbl.String() != want {
		t.Errorf("table =\n%s\nwant\n%s", tbl.String(), want)
	}
}

// leaves returns "key: value" for each scalar map entry in v.
func leaves(key string, v any) []string {
	switch x := v.(type) {
	case map[string]any:
		var out []string
		for k, val := range x {
			out = append(out, leaves(k, val)...)
		}
		return out
	case []any:
		var out []string
		for _, val := range x {
			out = append(out, leaves("", val)...)
		}
		return out
	case nil:
		return nil
	case string:
		if x == "" {
			x = `""`
		}
		if key == "" {
			return []string{x}
		}
		return []string{key + ": " + x}
	default:
		if key == "" {
			return []string{fmt.Sprint(x)}
		}
		return []string{key + ": " + fmt.Sprint(x)}
	}
}
