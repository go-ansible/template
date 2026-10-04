package template

import (
	"fmt"
	"math"
	"strings"

	pcre "github.com/go-regexp/engine"
	"github.com/nikolalohinski/gonja/v2/exec"
)

// testArgs normalises a test call's positional arguments.
//
// gonja hands a test its comma-separated arguments as ONE list value
// (`match('a', true)` arrives as a single ValuesList), unlike a filter
// call, which gets proper multiple Args. testVersion discovered this
// and worked around it inline; every test taking more than one
// argument needs the same, so it lives here once.
func testArgs(params *exec.VarArgs) []any {
	var out []any
	for i, a := range params.Args {
		if i == 0 && a.IsList() {
			if parts, ok := a.ToGoSimpleType(false).([]any); ok {
				out = append(out, parts...)
				continue
			}
		}
		out = append(out, a.ToGoSimpleType(false))
	}
	return out
}

// firstArgRaw is testArgs' counterpart for a test taking exactly ONE
// argument of any type. Flattening is ambiguous there: gonja packs
// `match('a', true)`'s two arguments into one list value, and
// `subset(nums)`'s single LIST argument looks exactly the same. A test
// that takes one argument must not flatten, or `[1,2] is subset([1,2,3])`
// compares against the scalar 1 and answers false -- measured against
// real, which answers true.
func firstArgRaw(params *exec.VarArgs) (any, bool) {
	if len(params.Args) == 0 {
		return nil, false
	}
	return params.Args[0].ToGoSimpleType(false), true
}

func argAt(args []any, i int) (any, bool) {
	if i < len(args) {
		return args[i], true
	}
	return nil, false
}

func argBoolAt(args []any, i int, params *exec.VarArgs, kw string, def bool) bool {
	if v, ok := argAt(args, i); ok {
		b, _ := v.(bool)
		return b
	}
	if v, ok := params.KwArgs[kw]; ok {
		return v.Bool()
	}
	return def
}

func argStrAt(args []any, i int, params *exec.VarArgs, kw, def string) string {
	if v, ok := argAt(args, i); ok {
		return fmt.Sprintf("%v", v)
	}
	if v, ok := params.KwArgs[kw]; ok {
		return v.String()
	}
	return def
}

// testRegex is real's `regex` test, which `match` and `search` are
// thin wrappers over:
//
//	regex(value, pattern, ignorecase=False, multiline=False, match_type='search')
//
// match_type is one of search/match/fullmatch and anything else is an
// error rather than a silent fallback to search -- real raises
// AnsibleTemplatePluginError naming the valid set.
//
// Python's re.match anchors at the START only, not the end, so it is
// not "pattern plus ^": a leftmost match beginning at 0 is a match,
// however short. FindStringIndex returns the leftmost match, so
// begin == 0 decides it. fullmatch additionally requires end == len.
func testRegex(defaultMatchType string) exec.TestFunction {
	return func(e *exec.Evaluator, in *exec.Value, params *exec.VarArgs) (bool, error) {
		args := testArgs(params)
		pattern := argStrAt(args, 0, params, "pattern", "")
		ignorecase := argBoolAt(args, 1, params, "ignorecase", false)
		multiline := argBoolAt(args, 2, params, "multiline", false)
		matchType := defaultMatchType
		if defaultMatchType == "" {
			matchType = argStrAt(args, 3, params, "match_type", "search")
		}
		switch matchType {
		case "search", "match", "fullmatch":
		default:
			return false, fmt.Errorf("Invalid match_type specified. Expected one of: search, match, fullmatch.")
		}

		// (?i) and (?m) are both honoured by this engine -- measured,
		// not assumed; (?s) is not supported and none of these tests
		// needs it.
		var prefix string
		if ignorecase {
			prefix += "(?i)"
		}
		if multiline {
			prefix += "(?m)"
		}
		re, err := pcre.Compile(prefix + pattern)
		if err != nil {
			return false, fmt.Errorf("%s: invalid pattern %q: %v", matchType, pattern, err)
		}
		s := in.String()
		loc := re.FindStringIndex(s)
		if loc == nil {
			return false, nil
		}
		switch matchType {
		case "search":
			return true, nil
		case "match":
			return loc[0] == 0, nil
		default: // fullmatch
			return loc[0] == 0 && loc[1] == len(s), nil
		}
	}
}

// testTruthy is real's truthy/falsy, including convert_bool, which
// turns "yes"/"on"/"false" and friends into a bool before testing.
func testTruthy(want bool) exec.TestFunction {
	return func(e *exec.Evaluator, in *exec.Value, params *exec.VarArgs) (bool, error) {
		args := testArgs(params)
		v := in.ToGoSimpleType(false)
		if argBoolAt(args, 0, params, "convert_bool", false) {
			if s, ok := v.(string); ok {
				switch strings.ToLower(strings.TrimSpace(s)) {
				case "y", "yes", "on", "1", "true", "t":
					v = true
				case "n", "no", "off", "0", "false", "f":
					v = false
				}
			}
		}
		return truthyValue(v) == want, nil
	}
}

// truthyValue is Python's bool(): empty string, empty collection, zero
// and nil are false; everything else is true.
func truthyValue(v any) bool {
	switch x := v.(type) {
	case nil:
		return false
	case bool:
		return x
	case string:
		return x != ""
	case int:
		return x != 0
	case int64:
		return x != 0
	case float64:
		return x != 0
	case []any:
		return len(x) > 0
	case map[string]any:
		return len(x) > 0
	default:
		return true
	}
}

// resultBoolTest is the shape every async/connectivity status test
// shares: the subject must be a mapping, and the named key decides.
// absentIs is what a non-async result reports -- real warns and
// answers true for started/finished, since a task that is neither is
// both.
func resultBoolTest(key string, absentIs bool, negate bool) exec.TestFunction {
	return func(e *exec.Evaluator, in *exec.Value, params *exec.VarArgs) (bool, error) {
		m, ok := in.ToGoSimpleType(false).(map[string]any)
		if !ok {
			return false, fmt.Errorf("the %q test expects a dictionary", key)
		}
		v, present := m[key]
		res := absentIs
		if present {
			res = truthyValue(v)
		}
		if negate {
			return !res, nil
		}
		return res, nil
	}
}

// testTimedout is not resultBoolTest: real requires the `timedout` key
// to be a mapping that itself carries a non-empty `period`.
func testTimedout(e *exec.Evaluator, in *exec.Value, params *exec.VarArgs) (bool, error) {
	m, ok := in.ToGoSimpleType(false).(map[string]any)
	if !ok {
		return false, fmt.Errorf("the \"timedout\" test expects a dictionary")
	}
	t, ok := m["timedout"].(map[string]any)
	if !ok {
		return false, nil
	}
	return truthyValue(t["period"]), nil
}

func asSlice(v any) ([]any, bool) {
	switch x := v.(type) {
	case []any:
		return x, true
	case string:
		out := make([]any, 0, len(x))
		for _, r := range x {
			out = append(out, string(r))
		}
		return out, true
	case map[string]any:
		out := make([]any, 0, len(x))
		for k := range x {
			out = append(out, k)
		}
		return out, true
	}
	return nil, false
}

func containsAll(outer, inner []any) bool {
	seen := make(map[string]bool, len(outer))
	for _, v := range outer {
		seen[fmt.Sprintf("%v", v)] = true
	}
	for _, v := range inner {
		if !seen[fmt.Sprintf("%v", v)] {
			return false
		}
	}
	return true
}

// testSubset/testSuperset are real's set(a) <= set(b) and set(a) >=
// set(b): a SET comparison, so duplicates and order do not count.
func testSubset(super bool) exec.TestFunction {
	return func(e *exec.Evaluator, in *exec.Value, params *exec.VarArgs) (bool, error) {
		other, ok := firstArgRaw(params)
		if !ok {
			return false, fmt.Errorf("the subset/superset tests expect one argument")
		}
		a, aok := asSlice(in.ToGoSimpleType(false))
		b, bok := asSlice(other)
		if !aok || !bok {
			return false, nil
		}
		if super {
			return containsAll(a, b), nil
		}
		return containsAll(b, a), nil
	}
}

// testContains is the `in` test with its arguments the other way
// round, so it composes with selectattr/rejectattr.
func testContains(e *exec.Evaluator, in *exec.Value, params *exec.VarArgs) (bool, error) {
	needle, ok := firstArgRaw(params)
	if !ok {
		return false, fmt.Errorf("the \"contains\" test expects one argument")
	}
	hay := in.ToGoSimpleType(false)
	if s, isStr := hay.(string); isStr {
		return strings.Contains(s, fmt.Sprintf("%v", needle)), nil
	}
	items, ok := asSlice(hay)
	if !ok {
		return false, nil
	}
	return containsAll(items, []any{needle}), nil
}

// testAnyAll are real's `any`/`all` over a sequence of truthy values.
func testAnyAll(wantAll bool) exec.TestFunction {
	return func(e *exec.Evaluator, in *exec.Value, params *exec.VarArgs) (bool, error) {
		items, ok := asSlice(in.ToGoSimpleType(false))
		if !ok {
			return false, nil
		}
		for _, it := range items {
			if truthyValue(it) != wantAll {
				return !wantAll, nil
			}
		}
		return wantAll, nil
	}
}

func testNaN(want bool) exec.TestFunction {
	return func(e *exec.Evaluator, in *exec.Value, params *exec.VarArgs) (bool, error) {
		f, ok := in.ToGoSimpleType(false).(float64)
		isNaN := ok && math.IsNaN(f)
		return isNaN == want, nil
	}
}

// registerMoreTests adds the rest of real's test library. Before this,
// `match`, `search` and `regex` were simply absent -- and because
// select/reject fall back to "the test said false", `reject('match',
// ...)` kept every element and `select('match', ...)` kept none, with
// no error either way. A silent wrong answer in one of the most common
// idioms real playbooks use.
func registerMoreTests(must func(string, exec.TestFunction)) {
	// regex family
	must("match", testRegex("match"))
	must("search", testRegex("search"))
	must("regex", testRegex(""))

	// truthiness
	must("truthy", testTruthy(true))
	must("falsy", testTruthy(false))

	// result status aliases this port did not have
	must("successful", testResultStatus(true))
	must("change", testResultFlag("changed"))
	must("skip", testResultFlag("skipped"))
	must("unreachable", resultBoolTest("unreachable", false, false))
	must("reachable", resultBoolTest("unreachable", false, true))
	must("timedout", testTimedout)
	// a non-async result is reported as both started and finished,
	// which is real's own answer (it warns and returns True).
	must("started", resultBoolTest("started", true, false))
	must("finished", resultBoolTest("finished", true, false))

	// set theory
	must("subset", testSubset(false))
	must("issubset", testSubset(false))
	must("superset", testSubset(true))
	must("issuperset", testSubset(true))
	must("contains", testContains)

	// lists
	must("any", testAnyAll(false))
	must("all", testAnyAll(true))

	// numbers
	must("nan", testNaN(true))
	must("isnan", testNaN(true))

	// version_compare is the name real gives the test this port
	// already had as `version`
	must("version_compare", testVersion)
}
