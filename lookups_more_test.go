package template

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// Every expected value below was MEASURED against ansible-core 2.21.4
// before it was written here, through a playbook using query()/wantlist
// so the dispatcher's own unwrap rule does not hide the plugin's list.

func TestLookupVarsMeasured(t *testing.T) {
	e := New()
	vars := map[string]any{
		"known_one": "first",
		"known_two": []any{1, 2},
		"templated": "value is {{ 1 + 1 }}",
		"plain":     "hello",
		"aa_one":    1,
	}

	for _, tc := range []struct {
		name string
		expr string
		want any
	}{
		// real: "first,third" -- two strings, comma-joined by the
		// dispatcher, which is why this asserts the list form instead.
		{"two names", `query('vars', 'known_one', 'plain')`, []any{"first", "hello"}},
		{"a list value survives", `query('vars', 'known_two')`, []any{[]any{1, 2}}},
		{"the value is TEMPLATED", `query('vars', 'templated')`, []any{"value is 2"}},
		{"default fills a hole, order kept", `query('vars', 'plain', 'missing_x', 'aa_one', default='D')`, []any{"hello", "D", 1}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := e.Eval(tc.expr, vars)
			if err != nil {
				t.Fatalf("Eval(%s): %v", tc.expr, err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("Eval(%s) = %#v, real gives %#v", tc.expr, got, tc.want)
			}
		})
	}
}

func TestLookupVarsErrors(t *testing.T) {
	e := New()
	vars := map[string]any{"plain": "hello"}

	// Real: "No variable named 'nope' was found." and NOT prefixed with
	// "The lookup plugin 'vars' failed" -- it is an undefined value
	// rather than a plugin failure, and the two read differently.
	_, err := e.Eval(`query('vars', 'nope')`, vars)
	if err == nil {
		t.Fatal("a missing name with no default must fail")
	}
	if !strings.Contains(err.Error(), "No variable named 'nope' was found.") {
		t.Errorf("missing name: %v", err)
	}
	if strings.Contains(err.Error(), lookupFailurePrefixFor("vars")) {
		t.Errorf("an undefined value must not be reported as a plugin failure: %v", err)
	}

	// Real: "The lookup plugin 'vars' failed: Variable name must be
	// 'str' not 'int'." -- prefixed, because this one raises.
	_, err = e.Eval(`query('vars', 7)`, vars)
	if err == nil {
		t.Fatal("a non-string name must fail")
	}
	if !strings.Contains(err.Error(), "Variable name must be 'str' not 'int'.") {
		t.Errorf("non-string name: %v", err)
	}
	if !strings.Contains(err.Error(), lookupFailurePrefixFor("vars")) {
		t.Errorf("a raised error must name the plugin: %v", err)
	}
}

func lookupFailurePrefixFor(name string) string {
	return "The lookup plugin '" + name + "' failed"
}

func TestLookupVarnamesMeasured(t *testing.T) {
	e := New()
	vars := map[string]any{"aa_one": 1, "aa_two": 2, "other_name": 3}

	for _, tc := range []struct {
		name string
		expr string
		want any
	}{
		{"unanchored SEARCH, not match", `query('varnames', 'a_one')`, []any{"aa_one"}},
		{"duplicates are kept, in term order", `query('varnames', '^aa_', 'aa_one')`, []any{"aa_one", "aa_two", "aa_one"}},
		{"no match is an empty list", `query('varnames', '^zzz')`, []any{}},
		{"names, never values", `query('varnames', '^other_')`, []any{"other_name"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := e.Eval(tc.expr, vars)
			if err != nil {
				t.Fatalf("Eval(%s): %v", tc.expr, err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("Eval(%s) = %#v, real gives %#v", tc.expr, got, tc.want)
			}
		})
	}

	// Real refuses an unusable pattern with its own sentence around
	// CPython's diagnostic; the sentence is what this port reproduces.
	if _, err := e.Eval(`query('varnames', '[')`, vars); err == nil {
		t.Error("an unusable pattern must fail")
	} else if !strings.Contains(err.Error(), `Unable to use "[" as a search parameter`) {
		t.Errorf("bad pattern: %v", err)
	}
}

func TestLookupLinesMeasured(t *testing.T) {
	e := New()

	for _, tc := range []struct {
		name string
		expr string
		want any
	}{
		{"three lines", `query('lines', 'printf "a\nb\nc\n"')`, []any{"a", "b", "c"}},
		{"no trailing newline", `query('lines', 'printf "x\ny"')`, []any{"x", "y"}},
		{"empty output is an EMPTY list", `query('lines', 'true')`, []any{}},
		{"a tab is not a separator", "query('lines', 'printf \"a b\tc\nd\n\"')", []any{"a b\tc", "d"}},
		{"a blank line in the middle is kept", `query('lines', 'printf "a\n\nb\n"')`, []any{"a", "", "b"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := e.Eval(tc.expr, map[string]any{})
			if err != nil {
				t.Fatalf("Eval(%s): %v", tc.expr, err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("Eval(%s) = %#v, real gives %#v", tc.expr, got, tc.want)
			}
		})
	}

	// Real: lookup_plugin.lines(exit 3) returned 3
	if _, err := e.Eval(`query('lines', 'exit 3')`, map[string]any{}); err == nil {
		t.Error("a failing command must fail the lookup")
	} else if !strings.Contains(err.Error(), "lookup_plugin.lines(exit 3) returned 3") {
		t.Errorf("failing command: %v", err)
	}
}

func TestLookupRandomChoiceMeasured(t *testing.T) {
	e := New()

	// Real returns a list of exactly ONE, whatever the term count.
	for i := 0; i < 50; i++ {
		got, err := e.Eval(`query('random_choice', 'a', 'b', 'c')`, map[string]any{})
		if err != nil {
			t.Fatalf("Eval: %v", err)
		}
		list, ok := got.([]any)
		if !ok || len(list) != 1 {
			t.Fatalf("got %#v, real gives a list of one", got)
		}
		if s, _ := list[0].(string); s != "a" && s != "b" && s != "c" {
			t.Fatalf("chose %#v, which is not one of the terms", list[0])
		}
	}

	// A single term, an empty term list, and a non-string term: all
	// measured, all returned as given.
	for _, tc := range []struct {
		expr string
		want any
	}{
		{`query('random_choice', 'only')`, []any{"only"}},
		{`query('random_choice')`, []any{}},
		{`query('random_choice', 5)`, []any{5}},
	} {
		got, err := e.Eval(tc.expr, map[string]any{})
		if err != nil {
			t.Fatalf("Eval(%s): %v", tc.expr, err)
		}
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("Eval(%s) = %#v, real gives %#v", tc.expr, got, tc.want)
		}
	}

	// A neuter guard: if the choice stopped varying, the loop above
	// would still pass. This asserts it VARIES, which is the property
	// the plugin exists for.
	seen := map[string]bool{}
	for i := 0; i < 200; i++ {
		got, err := e.Eval(`query('random_choice', 'a', 'b', 'c')`, map[string]any{})
		if err != nil {
			t.Fatalf("Eval: %v", err)
		}
		seen[got.([]any)[0].(string)] = true
	}
	if len(seen) != 3 {
		t.Errorf("200 draws from three terms produced %d distinct values, so the choice is not random", len(seen))
	}
}

func TestLookupFileglobMeasured(t *testing.T) {
	root := t.TempDir()
	files := filepath.Join(root, "files")
	if err := os.MkdirAll(filepath.Join(files, "adir"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"glob_a.txt", "glob_b.txt", "dup.txt"} {
		if err := os.WriteFile(filepath.Join(files, n), []byte(n), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// The SAME name one level up, which real never reports because the
	// search stops at the first directory that matched.
	if err := os.WriteFile(filepath.Join(root, "dup.txt"), []byte("basedir"), 0o644); err != nil {
		t.Fatal(err)
	}

	e := New()
	vars := map[string]any{"ansible_search_path": []any{root}}

	got, err := e.Eval(`query('fileglob', 'glob_*.txt')`, vars)
	if err != nil {
		t.Fatalf("Eval: %v", err)
	}
	if want := []any{filepath.Join(files, "glob_a.txt"), filepath.Join(files, "glob_b.txt")}; !reflect.DeepEqual(got, want) {
		t.Errorf("glob_*.txt = %#v, want %#v (ABSOLUTE paths, as real returns)", got, want)
	}

	// A glob matching only a DIRECTORY finds nothing -- measured.
	if got, err := e.Eval(`query('fileglob', 'a*')`, vars); err != nil {
		t.Fatalf("Eval: %v", err)
	} else if !reflect.DeepEqual(got, []any{}) {
		t.Errorf("a* = %#v, real excludes directories and gives []", got)
	}

	// The search STOPS at files/, so the copy in the basedir is never
	// reported. Two results here would mean the break was dropped.
	if got, err := e.Eval(`query('fileglob', 'dup.txt')`, vars); err != nil {
		t.Fatalf("Eval: %v", err)
	} else if want := []any{filepath.Join(files, "dup.txt")}; !reflect.DeepEqual(got, want) {
		t.Errorf("dup.txt = %#v, want %#v -- the search must stop at the first directory that matched", got, want)
	}

	// A term with a directory component: that directory is resolved
	// through the search path, and only the basename is globbed.
	if got, err := e.Eval(`query('fileglob', 'files/glob_b.txt')`, vars); err != nil {
		t.Fatalf("Eval: %v", err)
	} else if want := []any{filepath.Join(files, "glob_b.txt")}; !reflect.DeepEqual(got, want) {
		t.Errorf("files/glob_b.txt = %#v, want %#v", got, want)
	}

	if got, err := e.Eval(`query('fileglob', 'nothing_*.zzz')`, vars); err != nil {
		t.Fatalf("Eval: %v", err)
	} else if !reflect.DeepEqual(got, []any{}) {
		t.Errorf("no match = %#v, real gives []", got)
	}
}
