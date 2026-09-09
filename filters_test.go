package template

import (
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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
		{"checksum", "aaf4c61ddcc5e8a2dabede0f3b482cd9aea9434d"},
		// hash's own default hashtype is sha1 (matching real Ansible's
		// get_hash(data, hashtype='sha1')), not a fixed sha256.
		{"hash", "aaf4c61ddcc5e8a2dabede0f3b482cd9aea9434d"},
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

	got, err := e.Render(`{{ 'hello' | hash('sha256') }}`, nil)
	if err != nil {
		t.Fatal(err)
	}
	if want := "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824"; got != want {
		t.Errorf("hash('hello', 'sha256') = %q, want %q", got, want)
	}

	if _, err := e.Eval(`'x' | hash('not-a-real-algorithm')`, nil); err == nil {
		t.Fatal("hash with an unsupported algorithm name: got nil error, want one")
	}
}

func TestB64DecodeError(t *testing.T) {
	e := New()
	if _, err := e.Eval(`'not-valid-base64!!' | b64decode`, nil); err == nil {
		t.Fatal("b64decode on invalid input: got nil error, want one")
	}
}

func asAnySlice(vals ...any) []any { return vals }

func TestSetFilters(t *testing.T) {
	e := New()
	vars := map[string]any{"a": asAnySlice(1, 2, 3), "b": asAnySlice(2, 3, 4)}

	cases := []struct {
		expr string
		want []any
	}{
		{`a | union(b)`, asAnySlice(1, 2, 3, 4)},
		{`a | intersect(b)`, asAnySlice(2, 3)},
		{`a | difference(b)`, asAnySlice(1)},
		{`a | symmetric_difference(b)`, asAnySlice(1, 4)},
	}
	for _, c := range cases {
		t.Run(c.expr, func(t *testing.T) {
			got, err := e.Eval(c.expr, vars)
			if err != nil {
				t.Fatal(err)
			}
			list, ok := got.([]any)
			if !ok {
				t.Fatalf("%s = %#v (%T), want []any", c.expr, got, got)
			}
			if len(list) != len(c.want) {
				t.Fatalf("%s = %#v, want %#v", c.expr, list, c.want)
			}
			for i := range list {
				gotInt, _ := list[i].(int)
				wantInt, _ := c.want[i].(int)
				if gotInt != wantInt {
					t.Fatalf("%s = %#v, want %#v", c.expr, list, c.want)
				}
			}
		})
	}
}

func TestSetFiltersMissingArg(t *testing.T) {
	e := New()
	for _, expr := range []string{
		`a | union`, `a | intersect`, `a | difference`, `a | symmetric_difference`,
	} {
		if _, err := e.Eval(expr, map[string]any{"a": asAnySlice(1, 2)}); err == nil {
			t.Errorf("%s with no argument: got nil error, want one", expr)
		}
	}
}

func TestSetFiltersDedupWithinInput(t *testing.T) {
	e := New()
	got, err := e.Eval(`a | union(b)`, map[string]any{
		"a": asAnySlice(1, 1, 2),
		"b": asAnySlice(2, 3),
	})
	if err != nil {
		t.Fatal(err)
	}
	list := got.([]any)
	if len(list) != 3 {
		t.Fatalf("union with duplicate input = %#v, want 3 deduped elements", list)
	}
}

func TestLogPowRootFilters(t *testing.T) {
	e := New()
	cases := []struct {
		expr string
		want float64
	}{
		{`100 | log(10)`, 2},
		{`8 | log(2)`, 3},
		{`2 | pow(10)`, 1024},
		{`16 | root`, 4},
		// math.Pow(27, 1.0/3.0) lands a hair under 3 due to IEEE 754
		// rounding, same as real Python's 27 ** (1/3) — not a bug.
		{`27 | root(3)`, 3},
	}
	for _, c := range cases {
		t.Run(c.expr, func(t *testing.T) {
			got, err := e.Eval(c.expr, nil)
			if err != nil {
				t.Fatal(err)
			}
			f, ok := got.(float64)
			if !ok {
				t.Fatalf("%s = %#v (%T), want float64", c.expr, got, got)
			}
			if math.Abs(f-c.want) > 1e-9 {
				t.Errorf("%s = %v, want %v", c.expr, f, c.want)
			}
		})
	}

	if _, err := e.Eval(`2 | pow`, nil); err == nil {
		t.Fatal("pow with no argument: got nil error, want one")
	}

	got, err := e.Eval(`100 | log(base=10)`, nil)
	if err != nil {
		t.Fatal(err)
	}
	if f, _ := got.(float64); math.Abs(f-2) > 1e-9 {
		t.Errorf("log(base=10) = %v, want 2", got)
	}
}

func TestHumanReadableFilter(t *testing.T) {
	e := New()
	cases := []struct {
		expr string
		want string
	}{
		{`1024 | human_readable`, "1.00 KB"},
		{`1073741824 | human_readable`, "1.00 GB"},
		{`1024 | human_readable(unit='M')`, "0.00 MB"},
		{`1024 | human_readable(True)`, "1.00 Kb"},
		{`1024 | human_readable(False, 'M')`, "0.00 MB"},
	}
	for _, c := range cases {
		t.Run(c.expr, func(t *testing.T) {
			got, err := e.Render(`{{ `+c.expr+` }}`, nil)
			if err != nil {
				t.Fatal(err)
			}
			if got != c.want {
				t.Errorf("%s = %q, want %q", c.expr, got, c.want)
			}
		})
	}
}

func TestHumanToBytesFilter(t *testing.T) {
	e := New()
	cases := []struct {
		expr string
		want int64
	}{
		{`'1024' | human_to_bytes`, 1024},
		{`'1M' | human_to_bytes`, 1048576},
		{`'1MB' | human_to_bytes`, 1048576},
		{`'10' | human_to_bytes(default_unit='M')`, 10485760},
	}
	for _, c := range cases {
		t.Run(c.expr, func(t *testing.T) {
			got, err := e.Eval(c.expr, nil)
			if err != nil {
				t.Fatal(err)
			}
			n, ok := got.(int)
			if !ok {
				t.Fatalf("%s = %#v (%T), want int", c.expr, got, got)
			}
			if int64(n) != c.want {
				t.Errorf("%s = %v, want %v", c.expr, n, c.want)
			}
		})
	}

	if _, err := e.Eval(`'10Q' | human_to_bytes`, nil); err == nil {
		t.Fatal("human_to_bytes with an unknown unit suffix: got nil error, want one")
	}
	if _, err := e.Eval(`'not a number' | human_to_bytes`, nil); err == nil {
		t.Fatal("human_to_bytes on unparseable input: got nil error, want one")
	}
	if _, err := e.Eval(`'10Kb' | human_to_bytes`, nil); err == nil {
		t.Fatal("human_to_bytes with a bit unit but isbits=False: got nil error, want one")
	}

	got, err := e.Eval(`'10' | human_to_bytes('K', True)`, nil)
	if err != nil {
		t.Fatal(err)
	}
	if n, _ := got.(int); n != 10240 {
		t.Errorf("human_to_bytes('K', True) = %v, want 10240", got)
	}
}

func TestRekeyOnMemberFilter(t *testing.T) {
	e := New()
	list := []any{
		map[string]any{"name": "a", "id": 1},
		map[string]any{"name": "b", "id": 2},
	}

	got, err := e.RenderValue(`{{ l | rekey_on_member('name') }}`, map[string]any{"l": list})
	if err != nil {
		t.Fatal(err)
	}
	m := got.(map[string]any)
	if len(m) != 2 {
		t.Fatalf("rekey_on_member = %#v, want 2 entries", m)
	}
	if a := m["a"].(map[string]any); a["id"] != 1 {
		t.Fatalf(`rekey_on_member["a"] = %#v, want id=1`, a)
	}

	dup := []any{
		map[string]any{"name": "a", "id": 1},
		map[string]any{"name": "a", "id": 2},
	}
	if _, err := e.Eval(`l | rekey_on_member('name')`, map[string]any{"l": dup}); err == nil {
		t.Fatal("rekey_on_member with a duplicate key and duplicates=error: got nil error, want one")
	}

	got, err = e.RenderValue(`{{ l | rekey_on_member('name', duplicates='overwrite') }}`, map[string]any{"l": dup})
	if err != nil {
		t.Fatal(err)
	}
	m = got.(map[string]any)
	if a := m["a"].(map[string]any); a["id"] != 2 {
		t.Fatalf(`rekey_on_member overwrite ["a"] = %#v, want id=2 (last write wins)`, a)
	}

	if _, err := e.Eval(`l | rekey_on_member('missing')`, map[string]any{"l": list}); err == nil {
		t.Fatal("rekey_on_member with a key absent from every item: got nil error, want one")
	}
	if _, err := e.Eval(`l | rekey_on_member('name', duplicates='bogus')`, map[string]any{"l": list}); err == nil {
		t.Fatal("rekey_on_member with an unknown duplicates value: got nil error, want one")
	}
	if _, err := e.Eval(`l | rekey_on_member('name')`, map[string]any{"l": asAnySlice(1, 2)}); err == nil {
		t.Fatal("rekey_on_member on a list of non-dicts: got nil error, want one")
	}

	// A dict-of-dicts input rekeys the same way, over its values.
	got, err = e.RenderValue(`{{ d | rekey_on_member('name') }}`, map[string]any{
		"d": map[string]any{"first": map[string]any{"name": "a", "id": 1}},
	})
	if err != nil {
		t.Fatal(err)
	}
	m = got.(map[string]any)
	if a := m["a"].(map[string]any); a["id"] != 1 {
		t.Fatalf("rekey_on_member on dict-of-dicts = %#v", m)
	}
}

func TestToUUIDFilter(t *testing.T) {
	e := New()
	// Reference values computed with real Python's uuid.uuid5 against
	// Ansible's own default namespace UUID_NAMESPACE_ANSIBLE.
	got, err := e.Render(`{{ 'test' | to_uuid }}`, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got != "f4fb6740-9e00-5ac4-9581-f24b6ecfe71e" {
		t.Errorf("to_uuid('test') = %q, want %q", got, "f4fb6740-9e00-5ac4-9581-f24b6ecfe71e")
	}

	got, err = e.Render(`{{ 'ansible' | to_uuid }}`, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got != "5ecca099-0345-5f70-b25a-bacaea0dd909" {
		t.Errorf("to_uuid('ansible') = %q, want %q", got, "5ecca099-0345-5f70-b25a-bacaea0dd909")
	}

	got, err = e.Render(`{{ 'test' | to_uuid('6ba7b810-9dad-11d1-80b4-00c04fd430c8') }}`, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got != "4be0643f-1d98-573b-97cd-ca98a65347dd" {
		t.Errorf("to_uuid('test', custom namespace) = %q, want %q", got, "4be0643f-1d98-573b-97cd-ca98a65347dd")
	}

	if _, err := e.Eval(`'test' | to_uuid('not-a-uuid')`, nil); err == nil {
		t.Fatal("to_uuid with a malformed namespace: got nil error, want one")
	}
	if _, err := e.Eval(`'test' | to_uuid('gggggggg-gggg-gggg-gggg-gggggggggggg')`, nil); err == nil {
		t.Fatal("to_uuid with a right-length but non-hex namespace: got nil error, want one")
	}
}

func TestBasenameDirnameTrailingSlashAndEmpty(t *testing.T) {
	e := New()
	// Reference values are real Python's os.path.basename/dirname, NOT
	// Go's path.Base/path.Dir — the two disagree here (see filterBasename's
	// own comment), and this port matches Python since these filters exist
	// to reproduce Ansible's real behavior.
	cases := []struct {
		expr string
		want string
	}{
		{`'/foo/bar/' | basename`, ""},
		{`'/foo/bar/' | dirname`, "/foo/bar"},
		{`'foo' | dirname`, ""},
		{`'' | basename`, ""},
		{`'' | dirname`, ""},
	}
	for _, c := range cases {
		got, err := e.Render(`{{ `+c.expr+` }}`, nil)
		if err != nil {
			t.Fatal(err)
		}
		if got != c.want {
			t.Errorf("%s = %q, want %q", c.expr, got, c.want)
		}
	}
}

func TestPathJoinFilter(t *testing.T) {
	e := New()
	got, err := e.Render(`{{ ['/etc', 'foo', 'bar'] | path_join }}`, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got != "/etc/foo/bar" {
		t.Errorf("path_join(list) = %q, want %q", got, "/etc/foo/bar")
	}

	// An absolute component resets everything before it.
	got, err = e.Render(`{{ ['/etc', '/absolute', 'bar'] | path_join }}`, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got != "/absolute/bar" {
		t.Errorf("path_join(list with absolute reset) = %q, want %q", got, "/absolute/bar")
	}

	// A single string is an identity no-op, no join logic runs at all.
	got, err = e.Render(`{{ 'a/b' | path_join }}`, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got != "a/b" {
		t.Errorf("path_join(string) = %q, want %q", got, "a/b")
	}

	if _, err := e.Eval(`5 | path_join`, nil); err == nil {
		t.Fatal("path_join on neither a string nor a list: got nil error, want one")
	}
}

func TestSplitextFilter(t *testing.T) {
	e := New()
	cases := []struct {
		expr      string
		root, ext string
	}{
		{`'archive.tar.gz' | splitext`, "archive.tar", ".gz"},
		{`'.bashrc' | splitext`, ".bashrc", ""},
		{`'..bashrc' | splitext`, "..bashrc", ""},
		{`'a.' | splitext`, "a", "."},
		{`'/a/.bashrc' | splitext`, "/a/.bashrc", ""},
	}
	for _, c := range cases {
		got, err := e.RenderValue(`{{ `+c.expr+` }}`, nil)
		if err != nil {
			t.Fatal(err)
		}
		list, ok := got.([]any)
		if !ok || len(list) != 2 {
			t.Fatalf("%s = %#v, want a 2-element list", c.expr, got)
		}
		if list[0] != c.root || list[1] != c.ext {
			t.Errorf("%s = %#v, want (%q, %q)", c.expr, list, c.root, c.ext)
		}
	}
}

func TestNormpathFilter(t *testing.T) {
	e := New()
	cases := []struct{ expr, want string }{
		{`'a/b/../c' | normpath`, "a/c"},
		{`'./a/b/' | normpath`, "a/b"},
		{`'//foo' | normpath`, "//foo"},
		{`'///foo' | normpath`, "/foo"},
		{`'' | normpath`, "."},
		{`'/../foo' | normpath`, "/foo"},
	}
	for _, c := range cases {
		got, err := e.Render(`{{ `+c.expr+` }}`, nil)
		if err != nil {
			t.Fatal(err)
		}
		if got != c.want {
			t.Errorf("%s = %q, want %q", c.expr, got, c.want)
		}
	}
}

func TestCommonpathFilter(t *testing.T) {
	e := New()
	got, err := e.Render(`{{ ['/usr/lib', '/usr/local/lib'] | commonpath }}`, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got != "/usr" {
		t.Errorf("commonpath = %q, want %q", got, "/usr")
	}

	if _, err := e.Eval(`['a/b', '/a/c'] | commonpath`, nil); err == nil {
		t.Fatal("commonpath mixing absolute and relative paths: got nil error, want one")
	}
	if _, err := e.Eval(`[] | commonpath`, nil); err == nil {
		t.Fatal("commonpath on an empty list: got nil error, want one")
	}

	// A third, longer path (so the lexicographic min/max scan visits a
	// path that is neither the running min nor the running max) confirms
	// the result isn't an artifact of only ever comparing two paths.
	got, err = e.Render(`{{ ['/usr/lib/x', '/usr/local/lib', '/usr/lib'] | commonpath }}`, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got != "/usr" {
		t.Errorf("commonpath(3 paths) = %q, want %q", got, "/usr")
	}
}

func TestRelpathFilter(t *testing.T) {
	e := New()
	got, err := e.Render(`{{ '/a/b/c' | relpath('/a/x') }}`, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got != "../b/c" {
		t.Errorf("relpath = %q, want %q", got, "../b/c")
	}

	got, err = e.Render(`{{ '/a/b' | relpath('/a/b') }}`, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got != "." {
		t.Errorf("relpath of identical paths = %q, want %q", got, ".")
	}
}

func TestRealpathFilter(t *testing.T) {
	e := New()
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	if err := os.WriteFile(target, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}

	got, err := e.Eval(`p | realpath`, map[string]any{"p": link})
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := filepath.EvalSymlinks(target)
	if err != nil {
		t.Fatal(err)
	}
	if got != resolved {
		t.Errorf("realpath(symlink) = %q, want %q", got, resolved)
	}

	// A nonexistent path falls back to the plain absolute+normalized
	// path (EvalSymlinks errors on a missing component; see the
	// filterRealpath's own disclosed-simplification comment).
	missing := filepath.Join(dir, "does-not-exist")
	got, err = e.Eval(`p | realpath`, map[string]any{"p": missing})
	if err != nil {
		t.Fatal(err)
	}
	if got != missing {
		t.Errorf("realpath(nonexistent) = %q, want %q", got, missing)
	}
}

func TestExpandUserFilter(t *testing.T) {
	e := New()
	t.Setenv("HOME", "/home/testuser")

	got, err := e.Render(`{{ '~' | expanduser }}`, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got != "/home/testuser" {
		t.Errorf("expanduser('~') = %q, want %q", got, "/home/testuser")
	}

	got, err = e.Render(`{{ '~/x' | expanduser }}`, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got != "/home/testuser/x" {
		t.Errorf("expanduser('~/x') = %q, want %q", got, "/home/testuser/x")
	}

	// A path with no leading "~" is returned unchanged.
	got, err = e.Render(`{{ '/etc/x' | expanduser }}`, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got != "/etc/x" {
		t.Errorf("expanduser(no tilde) = %q, want %q", got, "/etc/x")
	}

	// An unresolvable "~user" falls back to the unchanged path, matching
	// real Python's own "unknown user: return unchanged" behavior.
	got, err = e.Render(`{{ '~this-user-should-not-exist-anywhere/x' | expanduser }}`, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got != "~this-user-should-not-exist-anywhere/x" {
		t.Errorf("expanduser(unknown user) = %q, want the path unchanged", got)
	}

	// With $HOME unset, expandUserHome falls back to the password
	// database (os/user.Current) rather than failing outright.
	t.Setenv("HOME", "")
	if _, err := e.Eval(`'~' | expanduser`, nil); err != nil {
		t.Fatal(err)
	}
}

func TestExpandVarsFilter(t *testing.T) {
	e := New()
	t.Setenv("FOO", "bar")

	cases := []struct{ expr, want string }{
		{`'${FOO}/x' | expandvars`, "bar/x"},
		{`'$FOO/x' | expandvars`, "bar/x"},
		{`'$NOTSET_XYZ/x' | expandvars`, "$NOTSET_XYZ/x"},
		{`'${NOTSET_XYZ/x' | expandvars`, "${NOTSET_XYZ/x"},
		{`'no vars here' | expandvars`, "no vars here"},
	}
	for _, c := range cases {
		got, err := e.Render(`{{ `+c.expr+` }}`, nil)
		if err != nil {
			t.Fatal(err)
		}
		if got != c.want {
			t.Errorf("%s = %q, want %q", c.expr, got, c.want)
		}
	}
}

func TestWinPathFilters(t *testing.T) {
	e := New()
	got, err := e.Render(`{{ 'foo/bar' | win_basename }}`, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got != "bar" {
		t.Errorf("win_basename = %q, want %q", got, "bar")
	}

	got, err = e.Render(`{{ 'C:\\foo\\bar' | win_dirname }}`, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got != `C:\foo` {
		t.Errorf("win_dirname = %q, want %q", got, `C:\foo`)
	}

	gotVal, err := e.RenderValue(`{{ 'C:\\foo\\bar' | win_splitdrive }}`, nil)
	if err != nil {
		t.Fatal(err)
	}
	list := gotVal.([]any)
	if list[0] != "C:" || list[1] != `\foo\bar` {
		t.Errorf("win_splitdrive = %#v, want (%q, %q)", list, "C:", `\foo\bar`)
	}

	gotVal, err = e.RenderValue(`{{ '\\\\server\\share\\a\\b' | win_splitdrive }}`, nil)
	if err != nil {
		t.Fatal(err)
	}
	list = gotVal.([]any)
	if list[0] != `\\server\share` || list[1] != `\a\b` {
		t.Errorf(`win_splitdrive(UNC) = %#v, want (%q, %q)`, list, `\\server\share`, `\a\b`)
	}

	gotVal, err = e.RenderValue(`{{ 'relative\\path' | win_splitdrive }}`, nil)
	if err != nil {
		t.Fatal(err)
	}
	list = gotVal.([]any)
	if list[0] != "" || list[1] != `relative\path` {
		t.Errorf("win_splitdrive(relative) = %#v, want (%q, %q)", list, "", `relative\path`)
	}

	// A rooted-relative path (no drive) and two malformed/incomplete UNC
	// forms (missing the share separator entirely).
	gotVal, err = e.RenderValue(`{{ '\\Windows' | win_splitdrive }}`, nil)
	if err != nil {
		t.Fatal(err)
	}
	list = gotVal.([]any)
	if list[0] != "" || list[1] != `\Windows` {
		t.Errorf("win_splitdrive(rooted, no drive) = %#v, want (%q, %q)", list, "", `\Windows`)
	}

	gotVal, err = e.RenderValue(`{{ '\\\\server' | win_splitdrive }}`, nil)
	if err != nil {
		t.Fatal(err)
	}
	list = gotVal.([]any)
	if list[0] != `\\server` || list[1] != "" {
		t.Errorf("win_splitdrive(UNC, no share separator) = %#v, want (%q, %q)", list, `\\server`, "")
	}

	gotVal, err = e.RenderValue(`{{ '\\\\server\\share' | win_splitdrive }}`, nil)
	if err != nil {
		t.Fatal(err)
	}
	list = gotVal.([]any)
	if list[0] != `\\server\share` || list[1] != "" {
		t.Errorf("win_splitdrive(UNC, no trailing separator) = %#v, want (%q, %q)", list, `\\server\share`, "")
	}
}

func TestCommentFilter(t *testing.T) {
	e := New()
	got, err := e.Render("{{ text | comment }}", map[string]any{"text": "hello\nworld"})
	if err != nil {
		t.Fatal(err)
	}
	if want := "#\n# hello\n# world\n#"; got != want {
		t.Errorf("comment(plain) = %q, want %q", got, want)
	}

	got, err = e.Render("{{ text | comment('cblock') }}", map[string]any{"text": "hello\nworld"})
	if err != nil {
		t.Fatal(err)
	}
	if want := "/*\n *\n * hello\n * world\n *\n */"; got != want {
		t.Errorf("comment(cblock) = %q, want %q", got, want)
	}

	got, err = e.Render("{{ text | comment('plain', decoration='## ') }}", map[string]any{"text": "hi"})
	if err != nil {
		t.Fatal(err)
	}
	if want := "##\n## hi\n##"; got != want {
		t.Errorf("comment with decoration override = %q, want %q", got, want)
	}

	if _, err := e.Eval(`'x' | comment('not-a-real-style')`, nil); err == nil {
		t.Fatal("comment with an unknown style: got nil error, want one")
	}

	// erlang/xml styles, style given as a keyword, and prefix_count > 1.
	got, err = e.Render("{{ 'hi' | comment(style='erlang') }}", nil)
	if err != nil {
		t.Fatal(err)
	}
	if want := "%\n% hi\n%"; got != want {
		t.Errorf("comment(style='erlang') = %q, want %q", got, want)
	}

	got, err = e.Render("{{ 'hi' | comment('xml') }}", nil)
	if err != nil {
		t.Fatal(err)
	}
	if want := "<!--\n -\n - hi\n -\n-->"; got != want {
		t.Errorf("comment('xml') = %q, want %q", got, want)
	}

	got, err = e.Render("{{ 'hi' | comment('plain', prefix_count=2) }}", nil)
	if err != nil {
		t.Fatal(err)
	}
	if want := "#\n#\n# hi\n#"; got != want {
		t.Errorf("comment(prefix_count=2) = %q, want %q", got, want)
	}

	// A blank line in the text produces a line that is just the
	// decorator, whose trailing space real Ansible strips.
	got, err = e.Render("{{ text | comment }}", map[string]any{"text": "a\n\nb"})
	if err != nil {
		t.Fatal(err)
	}
	if want := "#\n# a\n#\n# b\n#"; got != want {
		t.Errorf("comment with a blank line = %q, want %q", got, want)
	}
}

// tuplesEqual compares a []any of []any "tuples" (as every combinatorial
// filter below returns) against a matching [][]int reference for both
// content AND order — order is part of what's being verified, since these
// filters port Python's own specific generator algorithms, not just their
// result sets.
func tuplesEqual(t *testing.T, got any, want [][]int) {
	t.Helper()
	list, ok := got.([]any)
	if !ok {
		t.Fatalf("result type = %T, want []any", got)
	}
	if len(list) != len(want) {
		t.Fatalf("got %d tuples, want %d: %#v", len(list), len(want), list)
	}
	for i, w := range want {
		tuple, ok := list[i].([]any)
		if !ok || len(tuple) != len(w) {
			t.Fatalf("tuple %d = %#v, want %v", i, list[i], w)
		}
		for j, wv := range w {
			gv, _ := tuple[j].(int)
			if gv != wv {
				t.Fatalf("tuple %d = %#v, want %v", i, list[i], w)
			}
		}
	}
}

func TestProductFilter(t *testing.T) {
	e := New()

	got, err := e.Eval(`a | product(b)`, map[string]any{"a": asAnySlice(1, 2), "b": asAnySlice(3, 4)})
	if err != nil {
		t.Fatal(err)
	}
	tuplesEqual(t, got, [][]int{{1, 3}, {1, 4}, {2, 3}, {2, 4}})

	got, err = e.Eval(`a | product(repeat=2)`, map[string]any{"a": asAnySlice(1, 2)})
	if err != nil {
		t.Fatal(err)
	}
	tuplesEqual(t, got, [][]int{{1, 1}, {1, 2}, {2, 1}, {2, 2}})
}

func TestPermutationsFilter(t *testing.T) {
	e := New()

	got, err := e.Eval(`a | permutations(2)`, map[string]any{"a": asAnySlice(1, 2, 3)})
	if err != nil {
		t.Fatal(err)
	}
	tuplesEqual(t, got, [][]int{{1, 2}, {1, 3}, {2, 1}, {2, 3}, {3, 1}, {3, 2}})

	got, err = e.Eval(`a | permutations`, map[string]any{"a": asAnySlice(1, 2)})
	if err != nil {
		t.Fatal(err)
	}
	tuplesEqual(t, got, [][]int{{1, 2}, {2, 1}})

	got, err = e.Eval(`a | permutations(r=2)`, map[string]any{"a": asAnySlice(1, 2, 3)})
	if err != nil {
		t.Fatal(err)
	}
	tuplesEqual(t, got, [][]int{{1, 2}, {1, 3}, {2, 1}, {2, 3}, {3, 1}, {3, 2}})

	got, err = e.Eval(`a | permutations(0)`, map[string]any{"a": asAnySlice(1, 2)})
	if err != nil {
		t.Fatal(err)
	}
	tuplesEqual(t, got, [][]int{{}})

	got, err = e.Eval(`a | permutations(5)`, map[string]any{"a": asAnySlice(1, 2)})
	if err != nil {
		t.Fatal(err)
	}
	list := got.([]any)
	if len(list) != 0 {
		t.Errorf("permutations(r > n) = %#v, want an empty list", list)
	}
}

func TestCombinationsFilter(t *testing.T) {
	e := New()

	got, err := e.Eval(`a | combinations(2)`, map[string]any{"a": asAnySlice(1, 2, 3)})
	if err != nil {
		t.Fatal(err)
	}
	tuplesEqual(t, got, [][]int{{1, 2}, {1, 3}, {2, 3}})

	if _, err := e.Eval(`a | combinations`, map[string]any{"a": asAnySlice(1, 2, 3)}); err == nil {
		t.Fatal("combinations with no r argument: got nil error, want one (r is required)")
	}
}

func TestZipFilter(t *testing.T) {
	e := New()

	got, err := e.Eval(`a | zip(b)`, map[string]any{"a": asAnySlice(1, 2, 3), "b": asAnySlice(4, 5)})
	if err != nil {
		t.Fatal(err)
	}
	tuplesEqual(t, got, [][]int{{1, 4}, {2, 5}})
}

func TestZipLongestFilter(t *testing.T) {
	e := New()

	got, err := e.Eval(`a | zip_longest(b, fillvalue=0)`, map[string]any{"a": asAnySlice(1, 2, 3), "b": asAnySlice(4, 5)})
	if err != nil {
		t.Fatal(err)
	}
	tuplesEqual(t, got, [][]int{{1, 4}, {2, 5}, {3, 0}})

	gotVal, err := e.Eval(`a | zip_longest(b)`, map[string]any{"a": asAnySlice(1, 2, 3), "b": asAnySlice(4, 5)})
	if err != nil {
		t.Fatal(err)
	}
	list := gotVal.([]any)
	last := list[2].([]any)
	if last[0] != 3 || last[1] != nil {
		t.Errorf("zip_longest without fillvalue, last tuple = %#v, want (3, nil)", last)
	}
}

func TestExtractFilter(t *testing.T) {
	e := New()
	vars := map[string]any{
		"container": map[string]any{"a": map[string]any{"b": 3}},
		"list":      asAnySlice("x", "y", "z"),
	}

	got, err := e.Eval(`'a' | extract(container)`, vars)
	if err != nil {
		t.Fatal(err)
	}
	m, ok := got.(map[string]any)
	if !ok || m["b"] != 3 {
		t.Fatalf(`extract('a', container) = %#v, want {"b": 3}`, got)
	}

	got, err = e.Eval(`'a' | extract(container, 'b')`, vars)
	if err != nil {
		t.Fatal(err)
	}
	if got != 3 {
		t.Errorf(`extract('a', container, 'b') = %#v, want 3`, got)
	}

	got, err = e.Eval(`1 | extract(list)`, vars)
	if err != nil {
		t.Fatal(err)
	}
	if got != "y" {
		t.Errorf(`extract(1, list) = %#v, want "y"`, got)
	}

	got, err = e.Eval(`'missing' | extract(container)`, vars)
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Errorf(`extract('missing', container) = %#v, want nil`, got)
	}

	got, err = e.Eval(`'a' | extract(container, ['b'])`, vars)
	if err != nil {
		t.Fatal(err)
	}
	if got != 3 {
		t.Errorf(`extract('a', container, ['b']) = %#v, want 3 (morekeys as a list)`, got)
	}
}

func TestFlattenFilter(t *testing.T) {
	e := New()
	got, err := e.RenderValue(`{{ l | flatten }}`, map[string]any{
		"l": asAnySlice(1, asAnySlice(2, 3, asAnySlice(4, 5)), 6),
	})
	if err != nil {
		t.Fatal(err)
	}
	list := got.([]any)
	if len(list) != 6 {
		t.Fatalf("flatten (fully) = %#v, want 6 elements", list)
	}

	got, err = e.RenderValue(`{{ l | flatten(1) }}`, map[string]any{
		"l": asAnySlice(1, asAnySlice(2, asAnySlice(3, 4)), 5),
	})
	if err != nil {
		t.Fatal(err)
	}
	if list := got.([]any); len(list) != 4 {
		t.Fatalf("flatten(1) (positional) = %#v, want 4 elements", list)
	}

	got, err = e.RenderValue(`{{ l | flatten(levels=1) }}`, map[string]any{
		"l": asAnySlice(1, asAnySlice(2, asAnySlice(3, 4)), 5),
	})
	if err != nil {
		t.Fatal(err)
	}
	list = got.([]any)
	if len(list) != 4 {
		t.Fatalf("flatten(levels=1) = %#v, want 4 elements (one level deep)", list)
	}
	if inner, ok := list[2].([]any); !ok || len(inner) != 2 {
		t.Errorf("flatten(levels=1)[2] = %#v, want the still-nested [3,4]", list[2])
	}

	got, err = e.RenderValue(`{{ l | flatten(skip_nulls=False) }}`, map[string]any{
		"l": asAnySlice(1, nil, 2),
	})
	if err != nil {
		t.Fatal(err)
	}
	list = got.([]any)
	if len(list) != 3 {
		t.Fatalf("flatten(skip_nulls=False) = %#v, want nulls kept (3 elements)", list)
	}

	got, err = e.RenderValue(`{{ l | flatten }}`, map[string]any{
		"l": asAnySlice(1, nil, "None", "null", 2),
	})
	if err != nil {
		t.Fatal(err)
	}
	list = got.([]any)
	if len(list) != 2 {
		t.Errorf(`flatten (default skip_nulls) = %#v, want [1, 2] (nil/"None"/"null" all dropped)`, list)
	}
}

func TestSubelementsFilter(t *testing.T) {
	e := New()
	obj := asAnySlice(
		map[string]any{"name": "alice", "groups": asAnySlice("wheel", "docker")},
		map[string]any{"name": "bob", "groups": asAnySlice("docker")},
	)

	got, err := e.Eval(`obj | subelements('groups')`, map[string]any{"obj": obj})
	if err != nil {
		t.Fatal(err)
	}
	list := got.([]any)
	if len(list) != 3 {
		t.Fatalf("subelements = %#v, want 3 pairs (2 for alice, 1 for bob)", list)
	}
	pair, ok := list[0].([]any)
	if !ok || len(pair) != 2 {
		t.Fatalf("subelements[0] = %#v, want a 2-element pair", list[0])
	}
	elem := pair[0].(map[string]any)
	if elem["name"] != "alice" || pair[1] != "wheel" {
		t.Errorf("subelements[0] = %#v, want (alice, wheel)", pair)
	}

	if _, err := e.Eval(`obj | subelements('nosuchkey')`, map[string]any{"obj": obj}); err == nil {
		t.Fatal("subelements with a missing key and skip_missing=false: got nil error, want one")
	}

	got, err = e.Eval(`obj | subelements('nosuchkey', skip_missing=True)`, map[string]any{"obj": obj})
	if err != nil {
		t.Fatal(err)
	}
	if list := got.([]any); len(list) != 0 {
		t.Errorf("subelements with skip_missing=True = %#v, want an empty list", list)
	}

	// A dict-of-dicts input iterates over its values, same as a list.
	got, err = e.Eval(`obj | subelements('groups')`, map[string]any{
		"obj": map[string]any{"first": map[string]any{"name": "alice", "groups": asAnySlice("wheel")}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if list := got.([]any); len(list) != 1 {
		t.Errorf("subelements on dict-of-dicts = %#v, want 1 pair", list)
	}

	if _, err := e.Eval(`obj | subelements`, map[string]any{"obj": obj}); err == nil {
		t.Fatal("subelements with no accessor argument: got nil error, want one")
	}
	if _, err := e.Eval(`5 | subelements('x')`, nil); err == nil {
		t.Fatal("subelements on neither a list nor a dict: got nil error, want one")
	}
	if _, err := e.Eval(`obj | subelements('name')`, map[string]any{"obj": obj}); err == nil {
		t.Fatal("subelements where the accessor points to a non-list value: got nil error, want one")
	}
	if _, err := e.Eval(`obj | subelements('name.x')`, map[string]any{"obj": obj}); err == nil {
		t.Fatal("subelements descending through a non-dict value: got nil error, want one")
	}
}

func TestSplitFilter(t *testing.T) {
	e := New()
	cases := []struct {
		expr string
		want []string
	}{
		{`'abc def' | split`, []string{"abc", "def"}},
		{`'  a  b  c  ' | split`, []string{"a", "b", "c"}},
		{`'' | split`, []string{}},
		{`'a,b,,c' | split(',')`, []string{"a", "b", "", "c"}},
		{`'a,b,c' | split(',', 1)`, []string{"a", "b,c"}},
		{`'a b c d' | split(maxsplit=2)`, []string{"a", "b", "c d"}},
		{`'' | split('.')`, []string{""}},
	}
	for _, c := range cases {
		t.Run(c.expr, func(t *testing.T) {
			got, err := e.Eval(c.expr, nil)
			if err != nil {
				t.Fatal(err)
			}
			list, ok := got.([]any)
			if !ok || len(list) != len(c.want) {
				t.Fatalf("%s = %#v, want %v", c.expr, got, c.want)
			}
			for i, w := range c.want {
				if list[i] != w {
					t.Fatalf("%s = %#v, want %v", c.expr, got, c.want)
				}
			}
		})
	}
}

func TestFileglobFilter(t *testing.T) {
	e := New()
	dir := t.TempDir()
	for _, name := range []string{"a.txt", "b.txt", "c.log"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(dir, "d.txt"), 0o755); err != nil {
		t.Fatal(err)
	}

	got, err := e.Eval(`p | fileglob`, map[string]any{"p": filepath.Join(dir, "*.txt")})
	if err != nil {
		t.Fatal(err)
	}
	list, ok := got.([]any)
	if !ok {
		t.Fatalf("fileglob result type = %T, want []any", got)
	}
	names := make(map[string]bool, len(list))
	for _, m := range list {
		names[filepath.Base(m.(string))] = true
	}
	// a.txt and b.txt match and are regular files; d.txt matches the
	// pattern but is a directory, so os.path.isfile (here, IsRegular)
	// excludes it; c.log doesn't match *.txt at all.
	if len(names) != 2 || !names["a.txt"] || !names["b.txt"] {
		t.Errorf("fileglob(*.txt) = %#v, want exactly {a.txt, b.txt}", names)
	}

	got, err = e.Eval(`p | fileglob`, map[string]any{"p": filepath.Join(dir, "nosuchpattern-*.xyz")})
	if err != nil {
		t.Fatal(err)
	}
	if list := got.([]any); len(list) != 0 {
		t.Errorf("fileglob with no matches = %#v, want an empty list (not an error)", list)
	}

	if _, err := e.Eval(`'[' | fileglob`, nil); err == nil {
		t.Fatal("fileglob with a malformed pattern: got nil error, want one")
	}
}

func TestToDatetimeFilter(t *testing.T) {
	e := New()

	// Reference values are real Python's own
	// (datetime.strptime(s, fmt) - datetime(1970,1,1)).total_seconds().
	got, err := e.Eval(`'2021-06-15 13:45:30' | to_datetime`, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got != 1623764730.0 {
		t.Errorf("to_datetime(default format) = %v, want 1623764730.0", got)
	}

	got, err = e.Eval(`'2021-06-15' | to_datetime('%Y-%m-%d')`, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got != 1623715200.0 {
		t.Errorf("to_datetime(custom format) = %v, want 1623715200.0", got)
	}

	got, err = e.Eval(`'2021-06-15' | to_datetime(format='%Y-%m-%d')`, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got != 1623715200.0 {
		t.Errorf("to_datetime(format=...) = %v, want 1623715200.0", got)
	}

	// Subtracting two to_datetime results gives the same elapsed-seconds
	// value real Python's own (a - b).total_seconds() gives — the
	// primary real use case this filter's own float representation is
	// chosen to support (see filterToDatetime's own comment).
	got, err = e.Eval(`('2021-06-15 13:45:30' | to_datetime) - ('1970-01-01 00:00:00' | to_datetime)`, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got != 1623764730.0 {
		t.Errorf("to_datetime subtraction = %v, want 1623764730.0", got)
	}

	if _, err := e.Eval(`'not a date' | to_datetime`, nil); err == nil {
		t.Fatal("to_datetime on an unparseable string: got nil error, want one")
	}
	if _, err := e.Eval(`'2021-166' | to_datetime('%Y-%j')`, nil); err == nil {
		t.Fatal("to_datetime with %j (no Go layout equivalent): got nil error, want one")
	}
}

func TestStrftimeFilter(t *testing.T) {
	e := New()

	// Reference values are real Python's own
	// datetime.fromtimestamp(second, tz=utc).strftime(format).
	cases := []struct {
		expr string
		want string
	}{
		{`'%Y-%m-%d %H:%M:%S' | strftime(1623764730, utc=True)`, "2021-06-15 13:45:30"},
		{`'%Y-%m-%d %H:%M:%S.%f' | strftime(1623764730.5, utc=True)`, "2021-06-15 13:45:30.500000"},
		{`'%A %d %B %Y %I:%M %p' | strftime(1623764730.5, True)`, "Tuesday 15 June 2021 01:45 PM"},
		{`'%a %b %y' | strftime(1623764730.5, utc=True)`, "Tue Jun 21"},
		{`'%%literal%%' | strftime(1623764730, utc=True)`, "%literal%"},
	}
	for _, c := range cases {
		t.Run(c.expr, func(t *testing.T) {
			got, err := e.Eval(c.expr, nil)
			if err != nil {
				t.Fatal(err)
			}
			if got != c.want {
				t.Errorf("%s = %q, want %q", c.expr, got, c.want)
			}
		})
	}

	got, err := e.Eval(`'%Y' | strftime`, nil)
	if err != nil {
		t.Fatal(err)
	}
	if s, ok := got.(string); !ok || len(s) != 4 {
		t.Errorf("strftime with no second (current time) = %#v, want a 4-digit year", got)
	}

	if _, err := e.Eval(`'%j' | strftime(0, utc=True)`, nil); err == nil {
		t.Fatal("strftime with %j (no Go layout equivalent): got nil error, want one")
	}
}

// Reference values below are real `openssl passwd` output (OpenSSL 3.6.4)
// for the sha512/sha256/md5 cases, and the canonical OpenBSD bcrypt.c
// vectors for bcrypt — the same sources already used to validate
// go-encryptions/unixcrypt itself.

func TestPasswordHashFilter(t *testing.T) {
	e := New()

	// The DEFAULT hashtype (sha512) with no explicit rounds uses real
	// Ansible's own implicit default of 656000 — far above crypt(3)'s own
	// spec default of 5000 — so even this "no rounds given" case prints
	// a rounds= prefix, confirmed against real openssl.
	got, err := e.Eval(`'secret' | password_hash(salt='abc123')`, nil)
	if err != nil {
		t.Fatal(err)
	}
	if want := "$6$rounds=656000$abc123$diZ3d1OcpqciBvz3A9xUPag8FdoKzCId.Ok8txNszw9MLzlSeCHUz5PqoeKqmcCYz6py84HrbeQeEXPJEsZKi1"; got != want {
		t.Errorf("password_hash(default) = %q, want %q", got, want)
	}

	got, err = e.Eval(`'secret' | password_hash('sha256', 'abc123')`, nil)
	if err != nil {
		t.Fatal(err)
	}
	if want := "$5$rounds=535000$abc123$N68SJc6Sp0v/0Dw/aWgTkOjBJ9C4V2bsUELxlFaILfD"; got != want {
		t.Errorf("password_hash(sha256) = %q, want %q", got, want)
	}

	got, err = e.Eval(`'secret' | password_hash('md5', 'abc123')`, nil)
	if err != nil {
		t.Fatal(err)
	}
	if want := "$1$abc123$5IJcAgUIzNOMrV9cXyMFd1"; got != want {
		t.Errorf("password_hash(md5) = %q, want %q", got, want)
	}

	// sha512_crypt is an accepted spelling too (passlib's own name for
	// the same algorithm real Ansible's hashtype='sha512' maps to).
	got, err = e.Eval(`'secret' | password_hash('sha512_crypt', 'abc123', rounds=10000)`, nil)
	if err != nil {
		t.Fatal(err)
	}
	if want := "$6$rounds=10000$abc123$Z5HMbHsuGm9Y/O40xGGRe46SY51vnbt/dNLda1MMNMYi6vNmSYjFcGCre8GBI36M7KlPACMuZ7IiXkKj8OZRt/"; got != want {
		t.Errorf("password_hash(sha512_crypt, rounds=10000) = %q, want %q", got, want)
	}

	// rounds and salt_size given positionally rather than by keyword.
	got, err = e.Eval(`'secret' | password_hash('sha512', 'abc123', 16, 10000)`, nil)
	if err != nil {
		t.Fatal(err)
	}
	if want := "$6$rounds=10000$abc123$Z5HMbHsuGm9Y/O40xGGRe46SY51vnbt/dNLda1MMNMYi6vNmSYjFcGCre8GBI36M7KlPACMuZ7IiXkKj8OZRt/"; got != want {
		t.Errorf("password_hash(positional salt_size/rounds) = %q, want %q", got, want)
	}

	// salt_size affects fresh-salt generation length when no salt is given.
	got, err = e.Eval(`'secret' | password_hash('sha512', salt_size=4)`, nil)
	if err != nil {
		t.Fatal(err)
	}
	s, _ := got.(string)
	parts := strings.SplitN(s, "$", 5)
	if len(parts) < 4 || len(parts[3]) != 4 {
		t.Errorf("password_hash(salt_size=4) = %q, want a 4-character salt field", s)
	}
}

func TestPasswordHashBcrypt(t *testing.T) {
	e := New()

	// OpenBSD bcrypt.c's own canonical test vector, via password_hash's
	// bcrypt path: ident overridden to "2a" to match the vector's own
	// prefix (real Ansible's own default ident for bcrypt/blowfish is
	// "2b", the modern prefix every current implementation uses).
	got, err := e.Eval(`'U*U' | password_hash('bcrypt', 'CCCCCCCCCCCCCCCCCCCCC.', rounds=5, ident='2a')`, nil)
	if err != nil {
		t.Fatal(err)
	}
	if want := "$2a$05$CCCCCCCCCCCCCCCCCCCCC.E5YPO9kmyuRGyh0XouQYb4YMJKvyOeW"; got != want {
		t.Errorf("password_hash(bcrypt) = %q, want %q", got, want)
	}

	// blowfish is real Ansible's own alias for bcrypt.
	got, err = e.Eval(`'password' | password_hash('blowfish', 'cgT08pfGUo9SUIIvXrIJ1u', rounds=10)`, nil)
	if err != nil {
		t.Fatal(err)
	}
	if want := "$2b$10$cgT08pfGUo9SUIIvXrIJ1uSXp0VJmOIKgEC6vqGvqRddK4Z3JB28G"; got != want {
		t.Errorf("password_hash(blowfish) = %q, want %q", got, want)
	}

	// No explicit salt: a real 16-byte-random salt, well-formed output,
	// and two calls must not collide.
	got1, err := e.Eval(`'secret' | password_hash('bcrypt')`, nil)
	if err != nil {
		t.Fatal(err)
	}
	got2, err := e.Eval(`'secret' | password_hash('bcrypt')`, nil)
	if err != nil {
		t.Fatal(err)
	}
	s1, _ := got1.(string)
	if !strings.HasPrefix(s1, "$2b$12$") || len(s1) != 60 {
		t.Fatalf("password_hash(bcrypt, no salt) = %q, want a well-formed $2b$12$... hash of length 60", s1)
	}
	if got1 == got2 {
		t.Error("two password_hash(bcrypt) calls with no salt produced the same hash — suspicious for a real random salt")
	}
}

func TestPasswordHashFreshSalt(t *testing.T) {
	e := New()
	got1, err := e.Eval(`'secret' | password_hash('sha512')`, nil)
	if err != nil {
		t.Fatal(err)
	}
	got2, err := e.Eval(`'secret' | password_hash('sha512')`, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got1 == got2 {
		t.Error("two password_hash(sha512) calls with no salt produced the same hash — suspicious for a real random salt")
	}
	s1, _ := got1.(string)
	if !strings.HasPrefix(s1, "$6$rounds=656000$") {
		t.Errorf("password_hash(sha512, no salt) = %q, want a $6$rounds=656000$... hash", s1)
	}
}

func TestPasswordHashErrors(t *testing.T) {
	e := New()
	if _, err := e.Eval(`'secret' | password_hash('not-a-real-hashtype')`, nil); err == nil {
		t.Fatal("password_hash with an unsupported hashtype: got nil error, want one")
	}
	if _, err := e.Eval(`'secret' | password_hash('sha512', 'bad$salt')`, nil); err == nil {
		t.Fatal("password_hash with invalid characters in salt: got nil error, want one")
	}
	if _, err := e.Eval(`'secret' | password_hash('md5', 'waytoolongforasalt')`, nil); err == nil {
		t.Fatal("password_hash(md5) with an oversized salt: got nil error, want one")
	}
	if _, err := e.Eval(`'secret' | password_hash('bcrypt', 'tooshort')`, nil); err == nil {
		t.Fatal("password_hash(bcrypt) with a wrong-length salt: got nil error, want one")
	}
}
