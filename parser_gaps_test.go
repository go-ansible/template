package template

import (
	"strings"
	"testing"
)

// A method call on a PARENTHESISED expression does not parse. This is a
// gonja limitation, not something the filter or test registries can
// reach, and it is pinned here because the boundary is narrower and
// stranger than it first looks -- the parenthesis is what breaks it,
// not the filter:
//
//	d.keys()                        works
//	(d).keys()                      FAILS
//	(d | default({})).keys()        FAILS
//	d | default({}) | list          works
//	d | dict2items | map(...)       works
//
// Measured against ansible-core 2.21.4, where every one of those works.
// Found by accident: a module-census probe wrote
// `(r.extract_results | default({})).keys()` to compare a result's
// shape, and the engine failed the task rather than the module
// diverging.
//
// The working forms are pinned too, so the gap cannot widen into
// "nothing works".
func TestMethodCallOnParenthesisedExpression(t *testing.T) {
	e := New()
	d := map[string]any{"d": map[string]any{"a": 1, "b": 2}}

	for _, expr := range []string{
		`{{ d.keys() | sort | join(',') }}`,
		`{{ d | default({}) | list | sort | join(',') }}`,
		`{{ d | dict2items | map(attribute='key') | sort | join(',') }}`,
	} {
		got, err := e.Render(expr, d)
		if err != nil {
			t.Errorf("%s: should work, got %v", expr, err)
			continue
		}
		if got != "a,b" {
			t.Errorf("%s = %q, want a,b", expr, got)
		}
	}

	for _, expr := range []string{
		`{{ (d).keys() | sort | join(',') }}`,
		`{{ (d | default({})).keys() | sort | join(',') }}`,
	} {
		_, err := e.Render(expr, d)
		if err == nil {
			t.Logf("%s parses now -- gonja gained support for a method call on a "+
				"parenthesised expression, and this half of the test can go", expr)
			continue
		}
		// The gap is real; assert it is THIS gap and not some other
		// error, so a different failure does not hide behind it.
		if !strings.Contains(err.Error(), "not callable") {
			t.Errorf("%s failed for a different reason than the known gap: %v", expr, err)
		}
	}
}
