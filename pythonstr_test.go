package template

import "testing"

// TestPythonStr pins loop-label rendering against a real ansible-core
// 2.21.4 run: one looping task over eleven shapes, its `(item=...)`
// labels read straight off the transcript.
//
// The rule worth stating: str() of a CONTAINER uses repr() of its
// elements, so a bare string prints unquoted while the same string
// inside a list prints quoted.
func TestPythonStr(t *testing.T) {
	for _, tc := range []struct {
		in   any
		want string
	}{
		{"plain", "plain"},
		{5, "5"},
		{5.0, "5.0"},
		{true, "True"},
		{false, "False"},
		{nil, "None"},
		{[]any{"a", 1}, "['a', 1]"},
		{map[string]any{"k": "v"}, "{'k': 'v'}"},
		{[]any{}, "[]"},
		{map[string]any{}, "{}"},
		// Python switches to double quotes rather than escaping.
		{[]any{"it's"}, `["it's"]`},
		{map[string]any{"a": map[string]any{"b": []any{1}}}, "{'a': {'b': [1]}}"},
		// Nested None and floats go through repr too.
		{[]any{2, nil}, "[2, None]"},
		{[]any{1.0, 2.5}, "[1.0, 2.5]"},
	} {
		if got := PythonStr(tc.in); got != tc.want {
			t.Errorf("PythonStr(%#v) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestPythonReprQuotesABareString: the difference between Str and
// Repr is exactly the top-level string, and it is the one a loop label
// depends on.
func TestPythonReprQuotesABareString(t *testing.T) {
	if got := PythonRepr("plain"); got != "'plain'" {
		t.Errorf("PythonRepr = %q, want %q", got, "'plain'")
	}
	if got := PythonRepr(`a "quoted" one`); got != `'a "quoted" one'` {
		t.Errorf("PythonRepr = %q", got)
	}
	// Both kinds present: single quotes, with the single ones escaped.
	if got := PythonRepr(`it's "both"`); got != `'it\'s "both"'` {
		t.Errorf("PythonRepr = %q", got)
	}
}
