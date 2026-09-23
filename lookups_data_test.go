package template

import (
	"testing"
)

// TestDataLookups pins the pure lookup plugins — the set `with_<name>`
// needs — against values produced by RUNNING real ansible-core 2.21.4
// through query(), not by reading its plugin sources.
//
// Three of these read the opposite of how they sound, which is why
// they were measured: `list` does NOT flatten while `items` flattens
// one level; `together` pads with null (zip_longest) rather than
// stopping at the shortest; and `sequence` yields STRINGS.
func TestDataLookups(t *testing.T) {
	e := New()
	data := map[string]any{
		"d": map[string]any{"alpha": 1, "beta": 2},
		"people": []any{
			map[string]any{"name": "alice", "groups": []any{"wheel", "dev"}},
			map[string]any{"name": "bob", "groups": []any{"dev"}},
		},
	}
	for _, tc := range []struct{ expr, want string }{
		{`query('items', [1,2], 3)`, `[1, 2, 3]`},
		{`query('list', [1,2], 3)`, `[[1, 2], 3]`},
		{`query('flattened', [1,[2,[3]]], 4)`, `[1, 2, 3, 4]`},
		{`query('dict', d)`, `[{"key": "alpha", "value": 1}, {"key": "beta", "value": 2}]`},
		{`query('nested', [1,2], ['a','b'])`, `[[1, "a"], [1, "b"], [2, "a"], [2, "b"]]`},
		{`query('together', [1,2,3], ['a','b'])`, `[[1, "a"], [2, "b"], [3, null]]`},
		{`query('indexed_items', ['x','y'])`, `[[0, "x"], [1, "y"]]`},
		{`query('sequence', 'start=1 end=4')`, `["1", "2", "3", "4"]`},
		{`query('sequence', 'start=2 end=6 stride=2 format=n%d')`, `["n2", "n4", "n6"]`},
		{`query('sequence', 'start=1 count=3')`, `["1", "2", "3"]`},
		{`query('subelements', people, 'groups')`,
			`[[{"name": "alice"}, "wheel"], [{"name": "alice"}, "dev"], [{"name": "bob"}, "dev"]]`},
		{`query('subelements', [{'name':'x'}], 'groups', {'skip_missing': True})`, `[]`},
	} {
		got, err := e.Eval(tc.expr, data)
		if err != nil {
			t.Errorf("%s: %v", tc.expr, err)
			continue
		}
		js, err := ToJSON(got, 0)
		if err != nil {
			t.Errorf("%s: %v", tc.expr, err)
			continue
		}
		if js != tc.want {
			t.Errorf("%s =\n  %s\nwant\n  %s", tc.expr, js, tc.want)
		}
	}
}

// TestSubelementsStripsTheSubkey: the parent comes back WITHOUT the
// key the elements were taken from, so a template iterating the pair
// sees only the rest of the parent. Easy to miss, and a test that only
// checked the element would not catch it.
func TestSubelementsStripsTheSubkey(t *testing.T) {
	e := New()
	got, err := e.Eval(`query('subelements', people, 'groups')[0][0]`, map[string]any{
		"people": []any{map[string]any{"name": "alice", "groups": []any{"wheel"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	js, _ := ToJSON(got, 0)
	if js != `{"name": "alice"}` {
		t.Errorf("parent = %s, want it without the groups key", js)
	}
}

// TestSubelementsMissingKeyIsAnError without skip_missing — real
// refuses rather than skipping silently.
func TestSubelementsMissingKeyIsAnError(t *testing.T) {
	e := New()
	if _, err := e.Eval(`query('subelements', [{'name':'x'}], 'groups')`, nil); err == nil {
		t.Error("a missing subkey should be an error without skip_missing")
	}
}
