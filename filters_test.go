package template

import "testing"

func TestJSONFilters(t *testing.T) {
	e := New()

	got, err := e.Render(`{{ d | to_json }}`, map[string]any{"d": map[string]any{"a": 1}})
	if err != nil {
		t.Fatal(err)
	}
	if got != `{"a":1}` {
		t.Fatalf("to_json = %q", got)
	}

	got, err = e.Render(`{{ d | to_nice_json }}`, map[string]any{"d": map[string]any{"a": 1}})
	if err != nil {
		t.Fatal(err)
	}
	want := "{\n    \"a\": 1\n}"
	if got != want {
		t.Fatalf("to_nice_json = %q, want %q", got, want)
	}

	gotVal, err := e.RenderValue(`{{ s | from_json }}`, map[string]any{"s": `{"a":1,"b":[1,2,3]}`})
	if err != nil {
		t.Fatal(err)
	}
	m, ok := gotVal.(map[string]any)
	if !ok {
		t.Fatalf("from_json result type = %T, want map[string]any", gotVal)
	}
	if list, ok := m["b"].([]any); !ok || len(list) != 3 {
		t.Fatalf("from_json b = %#v, want a 3-element list", m["b"])
	}

	if _, err := e.Eval(`s | from_json`, map[string]any{"s": "not json"}); err == nil {
		t.Fatal("from_json on invalid JSON: got nil error, want one")
	}
}

func TestToJSONMarshalError(t *testing.T) {
	e := New()
	// A channel is a Go-native value json.Marshal cannot encode; RenderValue's
	// Go-level API accepts any map[string]any, including values that never
	// came from decoded YAML/JSON.
	if _, err := e.Eval(`d | to_json`, map[string]any{"d": make(chan int)}); err == nil {
		t.Fatal("to_json on an unmarshalable value: got nil error, want one")
	}
}

func TestYAMLFilters(t *testing.T) {
	e := New()

	got, err := e.Render(`{{ d | to_yaml }}`, map[string]any{"d": map[string]any{"a": 1}})
	if err != nil {
		t.Fatal(err)
	}
	if got != "a: 1" {
		t.Fatalf("to_yaml = %q, want %q", got, "a: 1")
	}

	// to_nice_yaml is registered as the same filter as to_yaml.
	if _, err := e.Render(`{{ d | to_nice_yaml }}`, map[string]any{"d": map[string]any{"a": 1}}); err != nil {
		t.Fatal(err)
	}

	gotVal, err := e.RenderValue(`{{ s | from_yaml }}`, map[string]any{"s": "a: 1\nb:\n  - 1\n  - 2\n"})
	if err != nil {
		t.Fatal(err)
	}
	m, ok := gotVal.(map[string]any)
	if !ok {
		t.Fatalf("from_yaml result type = %T, want map[string]any", gotVal)
	}
	if list, ok := m["b"].([]any); !ok || len(list) != 2 {
		t.Fatalf("from_yaml b = %#v, want a 2-element list", m["b"])
	}

	if _, err := e.Eval(`s | from_yaml`, map[string]any{"s": "a: [unterminated"}); err == nil {
		t.Fatal("from_yaml on invalid YAML: got nil error, want one")
	}
}

func TestRegexReplaceFilter(t *testing.T) {
	e := New()
	cases := []struct {
		name              string
		in, pattern, repl string
		want              string
	}{
		{"named group ref", "42", `(\d+)`, `N\g<1>`, "N42"},
		{"unterminated g-ref is left literal", "x", `x`, `N\g<1`, `N\g<1`},
		{"literal dollar in replacement", "a", `a`, `x$y`, "x$y"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := e.Eval(`v | regex_replace(pattern, repl)`, map[string]any{
				"v": c.in, "pattern": c.pattern, "repl": c.repl,
			})
			if err != nil {
				t.Fatal(err)
			}
			if got != c.want {
				t.Errorf("regex_replace(%q, %q, %q) = %q, want %q", c.in, c.pattern, c.repl, got, c.want)
			}
		})
	}

	if _, err := e.Eval(`'x' | regex_replace('a')`, nil); err == nil {
		t.Fatal("regex_replace with 1 arg: got nil error, want one")
	}
	if _, err := e.Eval(`'x' | regex_replace('(', 'y')`, nil); err == nil {
		t.Fatal("regex_replace with an invalid pattern: got nil error, want one")
	}
}

func TestRegexSearchFilter(t *testing.T) {
	e := New()
	got, err := e.Eval(`'hello world' | regex_search('w\\w+')`, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got != "world" {
		t.Fatalf("regex_search match = %#v, want %q", got, "world")
	}

	got, err = e.Eval(`'hello' | regex_search('zzz')`, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Fatalf("regex_search no match = %#v, want nil", got)
	}

	if _, err := e.Eval(`'x' | regex_search()`, nil); err == nil {
		t.Fatal("regex_search with no args: got nil error, want one")
	}
	if _, err := e.Eval(`'x' | regex_search('(')`, nil); err == nil {
		t.Fatal("regex_search with an invalid pattern: got nil error, want one")
	}
}

func TestRegexFindallFilter(t *testing.T) {
	e := New()
	got, err := e.Eval(`'a1 b2 c3' | regex_findall('\\d')`, nil)
	if err != nil {
		t.Fatal(err)
	}
	list, ok := got.([]any)
	if !ok || len(list) != 3 {
		t.Fatalf("regex_findall = %#v, want a 3-element list", got)
	}

	if _, err := e.Eval(`'x' | regex_findall()`, nil); err == nil {
		t.Fatal("regex_findall with no args: got nil error, want one")
	}
	if _, err := e.Eval(`'x' | regex_findall('(')`, nil); err == nil {
		t.Fatal("regex_findall with an invalid pattern: got nil error, want one")
	}
}

func TestRegexEscapeFilter(t *testing.T) {
	e := New()
	got, err := e.Eval(`'a.b*c' | regex_escape`, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got != `a\.b\*c` {
		t.Fatalf("regex_escape = %q, want %q", got, `a\.b\*c`)
	}
}

func TestBoolFilter(t *testing.T) {
	e := New()
	cases := []struct {
		expr string
		vars map[string]any
		want bool
	}{
		{`'yes' | bool`, nil, true},
		{`'On' | bool`, nil, true},
		{`'1' | bool`, nil, true},
		{`'no' | bool`, nil, false},
		{`'off' | bool`, nil, false},
		{`'0' | bool`, nil, false},
		{`'' | bool`, nil, false},
		{`' TRUE ' | bool`, nil, true},
		{`b | bool`, map[string]any{"b": true}, true},
		{`n | bool`, map[string]any{"n": 0}, false},
		{`'banana' | bool`, nil, true}, // unmatched string: falls through to truthiness
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

func TestMandatoryFilter(t *testing.T) {
	e := New()
	got, err := e.Eval(`x | mandatory`, map[string]any{"x": "set"})
	if err != nil {
		t.Fatal(err)
	}
	if got != "set" {
		t.Fatalf("mandatory(set) = %#v", got)
	}

	_, err = e.Eval(`x | mandatory`, map[string]any{"x": nil})
	if err == nil {
		t.Fatal("mandatory(nil): got nil error, want one")
	}

	_, err = e.Eval(`x | mandatory('custom message')`, map[string]any{"x": nil})
	if err == nil {
		t.Fatal("mandatory(nil, custom message): got nil error, want one")
	}
}

func TestTernaryFilterMissingArg(t *testing.T) {
	e := New()
	if _, err := e.Eval(`true | ternary('yes')`, nil); err == nil {
		t.Fatal("ternary with 1 arg: got nil error, want one")
	}
}

func TestCombineFilterRecursive(t *testing.T) {
	e := New()
	got, err := e.RenderValue("{{ a | combine(b, recursive=True) }}", map[string]any{
		"a": map[string]any{"x": map[string]any{"p": 1, "q": 2}},
		"b": map[string]any{"x": map[string]any{"q": 3, "r": 4}},
	})
	if err != nil {
		t.Fatal(err)
	}
	m := got.(map[string]any)
	nested, ok := m["x"].(map[string]any)
	if !ok {
		t.Fatalf("combine recursive result x = %#v, want a map", m["x"])
	}
	if nested["p"] != 1 || nested["q"] != 3 || nested["r"] != 4 {
		t.Fatalf("combine recursive nested = %#v", nested)
	}
}

func TestCombineFilterMultipleArgsAndNonMapIgnored(t *testing.T) {
	e := New()
	got, err := e.RenderValue("{{ a | combine(b, c) }}", map[string]any{
		"a": map[string]any{"x": 1},
		"b": 5, // not a map: ignored
		"c": map[string]any{"y": 2},
	})
	if err != nil {
		t.Fatal(err)
	}
	m := got.(map[string]any)
	if m["x"] != 1 || m["y"] != 2 {
		t.Fatalf("combine result = %#v", m)
	}
}

func TestCombineFilterNonMapInputIgnored(t *testing.T) {
	e := New()
	got, err := e.RenderValue("{{ a | combine(b) }}", map[string]any{
		"a": 5, // not a map: the input itself is skipped
		"b": map[string]any{"y": 2},
	})
	if err != nil {
		t.Fatal(err)
	}
	m := got.(map[string]any)
	if m["y"] != 2 || len(m) != 1 {
		t.Fatalf("combine result = %#v, want just {y: 2}", m)
	}
}

func TestDict2ItemsFilter(t *testing.T) {
	e := New()
	got, err := e.RenderValue(`{{ d | dict2items }}`, map[string]any{"d": map[string]any{"a": 1}})
	if err != nil {
		t.Fatal(err)
	}
	list := got.([]any)
	if len(list) != 1 {
		t.Fatalf("dict2items = %#v, want 1 item", list)
	}
	item := list[0].(map[string]any)
	if item["key"] != "a" || item["value"] != 1 {
		t.Fatalf("dict2items item = %#v, want key=a value=1", item)
	}

	got, err = e.RenderValue(`{{ d | dict2items('k', 'v') }}`, map[string]any{"d": map[string]any{"a": 1}})
	if err != nil {
		t.Fatal(err)
	}
	item = got.([]any)[0].(map[string]any)
	if item["k"] != "a" || item["v"] != 1 {
		t.Fatalf("dict2items with custom names = %#v, want k=a v=1", item)
	}
}

func TestItems2DictFilter(t *testing.T) {
	e := New()
	got, err := e.RenderValue(`{{ l | items2dict }}`, map[string]any{
		"l": []any{map[string]any{"key": "a", "value": 1}},
	})
	if err != nil {
		t.Fatal(err)
	}
	m := got.(map[string]any)
	if m["a"] != 1 {
		t.Fatalf("items2dict = %#v, want {a: 1}", m)
	}

	got, err = e.RenderValue(`{{ l | items2dict(key_name='k', value_name='v') }}`, map[string]any{
		"l": []any{map[string]any{"k": "a", "v": 1}, "not-a-dict"},
	})
	if err != nil {
		t.Fatal(err)
	}
	m = got.(map[string]any)
	if m["a"] != 1 || len(m) != 1 {
		t.Fatalf("items2dict with custom names (non-dict item skipped) = %#v", m)
	}
}

func TestTypeDebugFilter(t *testing.T) {
	e := New()
	got, err := e.Render(`{{ n | type_debug }}`, map[string]any{"n": 1})
	if err != nil {
		t.Fatal(err)
	}
	if got == "" {
		t.Fatal("type_debug returned an empty string")
	}
}

func TestQuoteFilter(t *testing.T) {
	e := New()
	cases := []struct {
		in, want string
	}{
		{"plain", "plain"},
		{"has space", "'has space'"},
		{"", "''"},
		{"a'b", `'a'"'"'b'`},
	}
	for _, c := range cases {
		got, err := e.Render(`{{ s | quote }}`, map[string]any{"s": c.in})
		if err != nil {
			t.Fatal(err)
		}
		if got != c.want {
			t.Errorf("quote(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestHashFilters(t *testing.T) {
	e := New()
	cases := []struct {
		filter string
		want   string
	}{
		{"md5", "5d41402abc4b2a76b9719d911017c592"},
		{"sha1", "aaf4c61ddcc5e8a2dabede0f3b482cd9aea9434d"},
		{"hash", "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824"},
	}
	for _, c := range cases {
		got, err := e.Render(`{{ 'hello' | `+c.filter+` }}`, nil)
		if err != nil {
			t.Fatal(err)
		}
		if got != c.want {
			t.Errorf("%s('hello') = %q, want %q", c.filter, got, c.want)
		}
	}
}

func TestB64DecodeError(t *testing.T) {
	e := New()
	if _, err := e.Eval(`'not-valid-base64!!' | b64decode`, nil); err == nil {
		t.Fatal("b64decode on invalid input: got nil error, want one")
	}
}
