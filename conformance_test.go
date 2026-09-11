package template

import "testing"

// The expectations in this file are not hand-derived: each one is the
// output real ansible-core 2.21.4 produced for the same expression, run
// through a real ansible-playbook against a local connection. Several of
// them contradict what this package's own tests previously asserted,
// which is why they exist — a reference value computed from reading the
// source can be wrong in exactly the way the source reading was.
func TestConformanceAgainstRealAnsible(t *testing.T) {
	e := New()

	cases := []struct {
		name string
		tmpl string
		vars map[string]any
		want string
	}{
		// type_debug reports PYTHON type names. This package used to
		// report Go's ("string", "[]interface {}", "map[string]interface
		// {}", "float64"), which silently breaks every real playbook
		// doing `when: x | type_debug == 'dict'`.
		{"type_debug str", `{{ 'x' | type_debug }}`, nil, "str"},
		{"type_debug int", `{{ 1 | type_debug }}`, nil, "int"},
		{"type_debug float", `{{ 1.5 | type_debug }}`, nil, "float"},
		{"type_debug bool", `{{ true | type_debug }}`, nil, "bool"},
		{"type_debug list", `{{ [] | type_debug }}`, nil, "list"},
		{"type_debug dict", `{{ {} | type_debug }}`, nil, "dict"},
		{"type_debug none", `{{ none | type_debug }}`, nil, "NoneType"},

		// ternary takes a third argument for the None case. Supplying it
		// used to be a hard error here.
		{"ternary none_val", `{{ none | ternary('T', 'F', 'N') }}`, nil, "N"},
		{"ternary true", `{{ true | ternary('T', 'F', 'N') }}`, nil, "T"},
		{"ternary false", `{{ false | ternary('T', 'F', 'N') }}`, nil, "F"},
		// Without a none_val, None falls through to the false branch —
		// real Ansible's own `if value is None and none_val is not None`.
		{"ternary none no none_val", `{{ none | ternary('T', 'F') }}`, nil, "F"},

		// Python's json.dumps separates with ", " and ": ", and honours
		// an indent argument.
		{"to_json separators", `{{ d | to_json }}`,
			map[string]any{"d": map[string]any{"a": 1, "b": 2}}, `{"a": 1, "b": 2}`},
		{"to_json list", `{{ l | to_json }}`,
			map[string]any{"l": []any{1, 2}}, `[1, 2]`},
		{"to_json indent", `{{ d | to_json(indent=2) }}`,
			map[string]any{"d": map[string]any{"a": 1}}, "{\n  \"a\": 1\n}"},
		{"to_json empty dict", `{{ {} | to_json }}`, nil, `{}`},
		// Go's encoding/json escapes <, > and & by default; Python's
		// json.dumps does not, so a URL, a shell redirect or a snippet of
		// markup used to come out mangled as \u003e and friends.
		{"to_json leaves angle brackets and ampersands alone",
			`{{ s | to_json }}`, map[string]any{"s": "a > b & c < d"}, `"a > b & c < d"`},
		{"to_json in a structure", `{{ d | to_json }}`,
			map[string]any{"d": map[string]any{"url": "x?a=1&b=2"}}, `{"url": "x?a=1&b=2"}`},
		{"to_json empty list", `{{ [] | to_json }}`, nil, `[]`},
		{"to_nice_json", `{{ d | to_nice_json }}`,
			map[string]any{"d": map[string]any{"a": 1}}, "{\n    \"a\": 1\n}"},

		// PyYAML's default_flow_style=None: a collection of nothing but
		// scalars comes out inline, anything nested stays block. The
		// dumper's trailing newline is part of the output.
		{"to_yaml flat is inline", `{{ d | to_yaml }}`,
			map[string]any{"d": map[string]any{"a": 1, "b": 2}}, "{a: 1, b: 2}\n"},
		{"to_yaml nested is block", `{{ d | to_yaml }}`,
			map[string]any{"d": map[string]any{"items": []any{1, 2}, "name": "x"}},
			"items: [1, 2]\nname: x\n"},
		{"to_nice_yaml is all block", `{{ d | to_nice_yaml }}`,
			map[string]any{"d": map[string]any{"a": 1, "b": 2}}, "a: 1\nb: 2\n"},

		// Go's math.Pow is a whole ULP away from the C pow() Python calls,
		// so this used to render 2.9999999999999996.
		{"root 3", `{{ 27 | root(3) }}`, nil, "3.0"},
		{"root 2", `{{ 16 | root }}`, nil, "4.0"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := e.Render(c.tmpl, c.vars)
			if err != nil {
				t.Fatal(err)
			}
			if got != c.want {
				t.Errorf("Render(%q) = %q, want %q (real ansible-core 2.21.4)", c.tmpl, got, c.want)
			}
		})
	}
}

// TestToJSONExported covers the emitter now shared with
// go-ansible/playbook's default callback, so a failure line there reads
// the way real Ansible's does rather than in Go's own compact spelling.
func TestToJSONExported(t *testing.T) {
	v := map[string]any{"b": 2, "a": 1, "l": []any{1, "x"}}

	got, err := ToJSON(v, 0)
	if err != nil {
		t.Fatal(err)
	}
	// Python's json.dumps default: ", " between items, ": " after a key.
	if want := `{"a": 1, "b": 2, "l": [1, "x"]}`; got != want {
		t.Errorf("ToJSON(v, 0) = %q, want %q", got, want)
	}

	got, err = ToJSON(map[string]any{"a": 1}, 4)
	if err != nil {
		t.Fatal(err)
	}
	if want := "{\n    \"a\": 1\n}"; got != want {
		t.Errorf("ToJSON(v, 4) = %q, want %q", got, want)
	}
}
