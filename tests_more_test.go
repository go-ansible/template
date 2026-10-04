package template

import "testing"

// Every expectation here was measured by running the same expression
// through a real ansible-playbook (ansible-core 2.21.4) and through
// this port's own binary, side by side.
//
// The reason this file exists at all: `match`, `search` and `regex`
// were not registered, and select/reject treat an unknown test as
// false rather than erroring. So `reject('match', ...)` kept every
// element and `select('match', ...)` kept none -- a silent wrong
// answer in one of the commonest idioms real playbooks use.
func TestJinjaTestsAgainstMeasuredReal(t *testing.T) {
	e := New()
	data := map[string]any{
		"xs":          []any{"alpha", "beta", "changed", "failed", "gamma"},
		"nums":        []any{1, 2, 3},
		"empty":       []any{},
		"res_ok":      map[string]any{"changed": true, "failed": false},
		"res_unreach": map[string]any{"unreachable": true},
	}
	for _, tc := range []struct{ expr, want string }{
		// the defect itself
		{`{{ xs | reject('match','^(changed|failed)$') | list | join(',') }}`, "alpha,beta,gamma"},
		{`{{ xs | select('match','^(al|ga)') | list | join(',') }}`, "alpha,gamma"},
		{`{{ xs | reject('search','a') | list | join(',') }}`, ""},

		// match anchors at the START only, like Python's re.match --
		// not "^pattern$", and not a full match either
		{`{{ 'abcdef' is match('abc') }}`, "True"},
		{`{{ 'xabc' is match('abc') }}`, "False"},
		{`{{ 'xabc' is search('abc') }}`, "True"},
		{`{{ 'abc' is regex('abc', false, false, 'fullmatch') }}`, "True"},
		{`{{ 'abcd' is regex('abc', false, false, 'fullmatch') }}`, "False"},
		{`{{ 'ABC' is match('abc', true) }}`, "True"},
		{`{{ 'ABC' is match('abc') }}`, "False"},

		{`{{ 'yes' is truthy }}`, "True"},
		{`{{ '' is truthy }}`, "False"},
		{`{{ '' is falsy }}`, "True"},

		// set comparisons, so duplicates and order do not count
		{`{{ [1,2] is subset(nums) }}`, "True"},
		{`{{ [1,9] is subset(nums) }}`, "False"},
		{`{{ nums is superset([1,2]) }}`, "True"},
		{`{{ nums is superset([9]) }}`, "False"},
		{`{{ nums is contains(2) }}`, "True"},
		{`{{ nums is contains(9) }}`, "False"},

		{`{{ nums is any }}`, "True"},
		{`{{ nums is all }}`, "True"},
		{`{{ [0,1] is all }}`, "False"},
		{`{{ empty is any }}`, "False"},

		{`{{ res_ok is successful }}`, "True"},
		{`{{ res_ok is change }}`, "True"},
		{`{{ res_unreach is unreachable }}`, "True"},
		{`{{ res_unreach is reachable }}`, "False"},

		// a NON-async result answers true to both, which is real's own
		// behaviour (it warns and returns True rather than false)
		{`{{ res_ok is started }}`, "True"},
		{`{{ res_ok is finished }}`, "True"},

		{`{{ '2.9' is version_compare('2.8','>') }}`, "True"},
	} {
		got, err := e.Render(tc.expr, data)
		if err != nil {
			t.Errorf("%s: %v", tc.expr, err)
			continue
		}
		if got != tc.want {
			t.Errorf("%s = %q, want %q (measured against real)", tc.expr, got, tc.want)
		}
	}
}

// A one-argument test must not have its argument flattened. gonja packs
// a test call's comma-separated arguments into a single list value, so
// `subset([1,2,3])`'s one LIST argument is indistinguishable from
// `match('a', true)`'s two. Flattening both made subset compare against
// the scalar 1 and answer false where real answers true.
func TestSingleArgumentTestsDoNotFlatten(t *testing.T) {
	e := New()
	d := map[string]any{"nums": []any{1, 2, 3}}
	for _, expr := range []string{
		`{{ [1,2] is subset(nums) }}`,
		`{{ nums is superset([1,2]) }}`,
	} {
		got, err := e.Render(expr, d)
		if err != nil {
			t.Fatalf("%s: %v", expr, err)
		}
		if got != "True" {
			t.Errorf("%s = %q, want True -- the list argument was flattened", expr, got)
		}
	}
}

// An unknown match_type is an error, not a quiet fall back to search.
func TestRegexRejectsAnUnknownMatchType(t *testing.T) {
	e := New()
	if _, err := e.Render(`{{ 'abc' is regex('abc', false, false, 'nonsense') }}`, nil); err == nil {
		t.Error("an unknown match_type was accepted")
	}
}

// KNOWN GAP, measured: gonja cannot parse a KEYWORD argument in a test
// call -- `match('abc', ignorecase=true)` is a parse error while
// `match('abc', true)` works. The test functions here already read
// params.KwArgs, so the keyword form starts working the day the parser
// allows it; until then the positional form is the one that does.
//
// This test pins the positional form so the gap cannot widen silently
// into "neither works".
func TestPositionalFormWorksWhileKeywordFormCannotParse(t *testing.T) {
	e := New()
	got, err := e.Render(`{{ 'ABC' is match('abc', true) }}`, nil)
	if err != nil || got != "True" {
		t.Fatalf("positional ignorecase = %q, %v", got, err)
	}
	if _, err := e.Render(`{{ 'ABC' is match('abc', ignorecase=true) }}`, nil); err == nil {
		t.Log("the keyword form parses now -- gonja gained support; this test can go")
	}
}
