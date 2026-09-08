package template

import (
	"reflect"
	"testing"
)

func TestRenderBasic(t *testing.T) {
	e := New()
	out, err := e.Render("hello {{ name }}!", map[string]any{"name": "world"})
	if err != nil {
		t.Fatal(err)
	}
	if out != "hello world!" {
		t.Fatalf("got %q", out)
	}
}

func TestWholeExpressionPreservesType(t *testing.T) {
	e := New()
	cases := []struct {
		name string
		tmpl string
		vars map[string]any
		want any
	}{
		{"int", "{{ n }}", map[string]any{"n": 42}, 42},
		{"bool", "{{ b }}", map[string]any{"b": true}, true},
		{"list", "{{ l }}", map[string]any{"l": []any{1, 2, 3}}, []any{1, 2, 3}},
		{"dict", "{{ d }}", map[string]any{"d": map[string]any{"a": 1}}, map[string]any{"a": int64(1)}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := e.RenderValue(c.tmpl, c.vars)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(normalizeInts(got), normalizeInts(c.want)) {
				t.Errorf("RenderValue(%q) = %#v (%T), want %#v (%T)", c.tmpl, got, got, c.want, c.want)
			}
		})
	}
}

// normalizeInts collapses int/int64/float64 differences so tests focus
// on value equality, not gonja's specific numeric representation.
func normalizeInts(v any) any {
	switch t := v.(type) {
	case int:
		return float64(t)
	case int64:
		return float64(t)
	case []any:
		out := make([]any, len(t))
		for i, e := range t {
			out[i] = normalizeInts(e)
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, e := range t {
			out[k] = normalizeInts(e)
		}
		return out
	default:
		return v
	}
}

func TestMixedStringStaysString(t *testing.T) {
	e := New()
	got, err := e.RenderValue("count={{ n }}", map[string]any{"n": 42})
	if err != nil {
		t.Fatal(err)
	}
	if got != "count=42" {
		t.Fatalf("got %#v, want string \"count=42\"", got)
	}
}

func TestRenderValueRecursesIntoStructures(t *testing.T) {
	e := New()
	in := map[string]any{
		"greeting": "hi {{ name }}",
		"nested": []any{
			map[string]any{"x": "{{ n }}"},
		},
	}
	got, err := e.RenderValue(in, map[string]any{"name": "bob", "n": 7})
	if err != nil {
		t.Fatal(err)
	}
	m := got.(map[string]any)
	if m["greeting"] != "hi bob" {
		t.Fatalf("greeting = %v", m["greeting"])
	}
	nested := m["nested"].([]any)[0].(map[string]any)
	if nested["x"] != 7 {
		t.Fatalf("nested x = %#v, want 7", nested["x"])
	}
}

func TestEvalBoolForWhen(t *testing.T) {
	e := New()
	cases := []struct {
		expr string
		vars map[string]any
		want bool
	}{
		{"x == 1", map[string]any{"x": 1}, true},
		{"x == 1", map[string]any{"x": 2}, false},
		{"x is defined", map[string]any{"x": 1}, true},
		{"y is not defined", map[string]any{"x": 1}, true},
		{"x and y", map[string]any{"x": true, "y": false}, false},
		{"x or y", map[string]any{"x": false, "y": true}, true},
		{"not x", map[string]any{"x": false}, true},
	}
	for _, c := range cases {
		t.Run(c.expr, func(t *testing.T) {
			got, err := e.EvalBool(c.expr, c.vars)
			if err != nil {
				t.Fatal(err)
			}
			if got != c.want {
				t.Errorf("EvalBool(%q) = %v, want %v", c.expr, got, c.want)
			}
		})
	}
}

func TestDefaultFilter(t *testing.T) {
	e := New()
	got, err := e.Render("{{ missing | default('fallback') }}", map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	if got != "fallback" {
		t.Fatalf("got %q", got)
	}
}

func TestAnsibleFilters(t *testing.T) {
	e := New()
	cases := []struct {
		name string
		tmpl string
		vars map[string]any
		want string
	}{
		{"to_json", `{{ d | to_json }}`, map[string]any{"d": map[string]any{"a": 1}}, `{"a":1}`},
		{"regex_replace", `{{ 'hello world' | regex_replace('world', 'there') }}`, nil, "hello there"},
		{"regex_replace backref", `{{ 'foo123' | regex_replace('foo(\\d+)', 'bar\\1') }}`, nil, "bar123"},
		{"basename", `{{ '/a/b/c.txt' | basename }}`, nil, "c.txt"},
		{"dirname", `{{ '/a/b/c.txt' | dirname }}`, nil, "/a/b"},
		{"b64encode", `{{ 'hi' | b64encode }}`, nil, "aGk="},
		{"b64decode", `{{ 'aGk=' | b64decode }}`, nil, "hi"},
		{"ternary true", `{{ true | ternary('yes', 'no') }}`, nil, "yes"},
		{"ternary false", `{{ false | ternary('yes', 'no') }}`, nil, "no"},
		{"bool yes", `{{ 'yes' | bool }}`, nil, "True"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := e.Render(c.tmpl, c.vars)
			if err != nil {
				t.Fatal(err)
			}
			if got != c.want {
				t.Errorf("Render(%q) = %q, want %q", c.tmpl, got, c.want)
			}
		})
	}
}

func TestCombineFilter(t *testing.T) {
	e := New()
	got, err := e.RenderValue("{{ a | combine(b) }}", map[string]any{
		"a": map[string]any{"x": 1, "y": 2},
		"b": map[string]any{"y": 3, "z": 4},
	})
	if err != nil {
		t.Fatal(err)
	}
	m := got.(map[string]any)
	if m["x"] != 1 || m["y"] != 3 || m["z"] != 4 {
		t.Fatalf("combine result = %#v", m)
	}
}

func TestVersionTest(t *testing.T) {
	e := New()
	cases := []struct {
		expr string
		want bool
	}{
		{"'2.10.1' is version('2.9', '>=')", true},
		{"'2.9.1' is version('2.10', '>=')", false},
		{"'2.10.0' is version('2.10.0', '==')", true},
	}
	for _, c := range cases {
		got, err := e.EvalBool(c.expr, nil)
		if err != nil {
			t.Fatal(err)
		}
		if got != c.want {
			t.Errorf("EvalBool(%q) = %v, want %v", c.expr, got, c.want)
		}
	}
}

func TestResultTests(t *testing.T) {
	e := New()
	result := map[string]any{"changed": true, "failed": false, "rc": 0}
	if got, _ := e.EvalBool("r is changed", map[string]any{"r": result}); !got {
		t.Error("expected r is changed == true")
	}
	if got, _ := e.EvalBool("r is success", map[string]any{"r": result}); !got {
		t.Error("expected r is success == true")
	}
	if got, _ := e.EvalBool("r is failed", map[string]any{"r": result}); got {
		t.Error("expected r is failed == false")
	}
}

func TestWholeExpressionEdgeCases(t *testing.T) {
	cases := []struct {
		name   string
		s      string
		wantOK bool
	}{
		{"multiple blocks not whole", "{{ a }}{{ b }}", false},
		{"trailing text not whole", "{{ a }} extra", false},
		{"empty string", "", false},
		{"unterminated", "{{ a", false},
		{"single expression with surrounding space", "  {{ a }}  ", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, ok := wholeExpression(c.s)
			if ok != c.wantOK {
				t.Errorf("wholeExpression(%q) ok = %v, want %v", c.s, ok, c.wantOK)
			}
		})
	}
}

func TestRenderParseError(t *testing.T) {
	e := New()
	if _, err := e.Render("{{ 1 + }}", nil); err == nil {
		t.Fatal("Render with a syntax error: got nil error, want one")
	}
}

func TestRenderExecuteError(t *testing.T) {
	e := New()
	if _, err := e.Render("{{ 1 | nosuchfilter }}", nil); err == nil {
		t.Fatal("Render with an undefined filter: got nil error, want one")
	}
}

func TestEvalParseError(t *testing.T) {
	e := New()
	if _, err := e.Eval("1 +", nil); err == nil {
		t.Fatal("Eval with a syntax error: got nil error, want one")
	}
}

func TestEvalRuntimeError(t *testing.T) {
	e := New()
	if _, err := e.Eval("1 | nosuchfilter", nil); err == nil {
		t.Fatal("Eval with an undefined filter: got nil error, want one")
	}
}

func TestEvalBoolParseError(t *testing.T) {
	e := New()
	if _, err := e.EvalBool("1 +", nil); err == nil {
		t.Fatal("EvalBool with a syntax error: got nil error, want one")
	}
}

func TestRenderValueScalarPassthrough(t *testing.T) {
	e := New()
	for _, v := range []any{42, true, nil, 3.14} {
		got, err := e.RenderValue(v, nil)
		if err != nil {
			t.Fatalf("RenderValue(%#v): %v", v, err)
		}
		if got != v {
			t.Errorf("RenderValue(%#v) = %#v, want unchanged", v, got)
		}
	}
}

func TestRenderValueNonTemplateStringPassthrough(t *testing.T) {
	e := New()
	got, err := e.RenderValue("plain string, no delimiters", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got != "plain string, no delimiters" {
		t.Errorf("RenderValue on a non-template string = %#v, want it unchanged", got)
	}
}

func TestRenderValueErrorPropagatesFromMap(t *testing.T) {
	e := New()
	_, err := e.RenderValue(map[string]any{"bad": "{{ 1 | nosuchfilter }}"}, nil)
	if err == nil {
		t.Fatal("RenderValue on a map with a bad nested template: got nil error, want one")
	}
}

func TestRenderValueErrorPropagatesFromList(t *testing.T) {
	e := New()
	_, err := e.RenderValue([]any{"{{ 1 | nosuchfilter }}"}, nil)
	if err == nil {
		t.Fatal("RenderValue on a list with a bad nested template: got nil error, want one")
	}
}

func TestOmitDropsMapKey(t *testing.T) {
	e := New()
	got, err := e.RenderValue(map[string]any{
		"present": "{{ x | default(omit) }}",
		"missing": "{{ y | default(omit) }}",
	}, map[string]any{"x": "set"})
	if err != nil {
		t.Fatal(err)
	}
	m := got.(map[string]any)
	if m["present"] != "set" {
		t.Errorf(`m["present"] = %#v, want "set"`, m["present"])
	}
	if _, ok := m["missing"]; ok {
		t.Errorf(`m["missing"] = %#v, want the key entirely absent`, m["missing"])
	}
	if len(m) != 1 {
		t.Errorf("map = %#v, want exactly 1 key", m)
	}
}

func TestOmitDropsListItem(t *testing.T) {
	e := New()
	got, err := e.RenderValue([]any{
		"a", "{{ y | default(omit) }}", "b",
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	list := got.([]any)
	if len(list) != 2 || list[0] != "a" || list[1] != "b" {
		t.Errorf("list = %#v, want [a b] (the omitted item dropped)", list)
	}
}

func TestOmitBooleanDefault(t *testing.T) {
	// default(omit, true) only substitutes when the input is falsy, not
	// merely undefined — gonja's own filterDefault already implements
	// this generically (Omit is just whatever "default value" argument
	// was given), so this doubles as regression coverage that the omit
	// global doesn't need special-casing inside the filter itself.
	e := New()
	got, err := e.RenderValue(map[string]any{
		"falsy":  "{{ x | default(omit, true) }}",
		"truthy": "{{ y | default(omit, true) }}",
	}, map[string]any{"x": "", "y": "set"})
	if err != nil {
		t.Fatal(err)
	}
	m := got.(map[string]any)
	if _, ok := m["falsy"]; ok {
		t.Errorf(`m["falsy"] = %#v, want omitted (empty string is falsy)`, m["falsy"])
	}
	if m["truthy"] != "set" {
		t.Errorf(`m["truthy"] = %#v, want "set"`, m["truthy"])
	}
}

func TestOmitNestedInStructure(t *testing.T) {
	e := New()
	got, err := e.RenderValue(map[string]any{
		"outer": map[string]any{
			"keep": "{{ x }}",
			"drop": "{{ y | default(omit) }}",
		},
	}, map[string]any{"x": "v"})
	if err != nil {
		t.Fatal(err)
	}
	outer := got.(map[string]any)["outer"].(map[string]any)
	if outer["keep"] != "v" {
		t.Errorf(`outer["keep"] = %#v`, outer["keep"])
	}
	if _, ok := outer["drop"]; ok {
		t.Errorf(`outer["drop"] = %#v, want omitted`, outer["drop"])
	}
}

func TestOmitBareTopLevelIsError(t *testing.T) {
	e := New()
	if _, err := e.RenderValue("{{ omit }}", nil); err == nil {
		t.Fatal("RenderValue on a bare omit with nothing to omit it from: got nil error, want one")
	}
	// The same value reached through Eval (no container-dropping concept
	// at that level) is not an error — it's a legitimate native value.
	got, err := e.Eval("omit", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !IsOmit(got) {
		t.Errorf("Eval(\"omit\") = %#v, want the omit sentinel", got)
	}
}

func TestIsTemplate(t *testing.T) {
	if !IsTemplate("{{ x }}") {
		t.Error("{{ x }} should be a template")
	}
	if !IsTemplate("{% if x %}y{% endif %}") {
		t.Error("{% if %} should be a template")
	}
	if IsTemplate("plain string") {
		t.Error("plain string should not be a template")
	}
}
