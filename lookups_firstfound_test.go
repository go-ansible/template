package template

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// firstFoundTree lays out the same files the reference probe used, so
// the expected values below are the ones ansible-core 2.21.4 produced
// for the same shapes.
func firstFoundTree(t *testing.T) (root string, vars map[string]any) {
	t.Helper()
	root = t.TempDir()
	for _, d := range []string{"files", "other"} {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	write := func(p, s string) {
		if err := os.WriteFile(filepath.Join(root, p), []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("files/one.txt", "A\n")
	write("files/two.txt", "B\n")
	write("other/three.txt", "C\n")
	// Real reports a symlink as ITSELF, never as its target --
	// unfrackpath(follow=False). Without a link here, nothing tells
	// the two apart.
	if err := os.Symlink(filepath.Join(root, "files", "one.txt"), filepath.Join(root, "link.txt")); err != nil {
		t.Fatal(err)
	}
	return root, map[string]any{"ansible_search_path": []any{root}}
}

func TestLookupFirstFoundMeasured(t *testing.T) {
	root, vars := firstFoundTree(t)
	e := New()

	base := func(t *testing.T, expr string) []string {
		t.Helper()
		got, err := e.Eval(expr, vars)
		if err != nil {
			t.Fatalf("Eval(%s): %v", expr, err)
		}
		list, ok := got.([]any)
		if !ok {
			t.Fatalf("Eval(%s) = %#v, want a list", expr, got)
		}
		out := make([]string, 0, len(list))
		for _, e := range list {
			s, _ := e.(string)
			if !filepath.IsAbs(s) {
				t.Errorf("Eval(%s) gave %q, which is not absolute -- real returns unfrackpath()", expr, s)
			}
			out = append(out, filepath.Base(s))
		}
		return out
	}

	for _, tc := range []struct {
		name string
		expr string
		want []string
	}{
		{"a plain string term", `query('first_found', 'one.txt')`, []string{"one.txt"}},
		{"the FIRST that exists wins", `query('first_found', 'nope.txt', 'two.txt', 'one.txt')`, []string{"two.txt"}},
		{"a dict term with files", `query('first_found', {'files': ['nope.txt', 'two.txt']})`, []string{"two.txt"}},
		{"a dict term with files and paths", `query('first_found', {'files': ['three.txt'], 'paths': ['other']})`, []string{"three.txt"}},
		{"a LIST term is flattened", `query('first_found', ['nope.txt', 'one.txt'])`, []string{"one.txt"}},
		{"files as a kwarg, no terms", `query('first_found', files=['nope.txt','two.txt'])`, []string{"two.txt"}},
		{"paths as a kwarg", `query('first_found', 'three.txt', paths=['other'])`, []string{"three.txt"}},
		// THE discriminating input: path-major would give three.txt,
		// because other/three.txt exists and other/one.txt does not.
		// Real gives one.txt, so the file list is the outer loop.
		// Two readings that only these four cases separate.
		//
		// Across the `files` OPTION the search is TERM-major: real
		// hands that list to _process_terms as terms, so each file is
		// tried against every path before the next file. Path-major
		// would give three.txt, which exists under other/.
		{"the files option is TERM-major",
			`query('first_found', files=['one.txt','three.txt'], paths=['other','files'])`,
			[]string{"one.txt"}},
		// Inside ONE dict term it is the other way round: paths are the
		// outer loop, so the first PATH that holds any of the files
		// wins. Measured both ways round, because a single ordering
		// could be explained by either rule.
		{"inside one dict term it is PATH-major",
			`query('first_found', {'files': ['one.txt','three.txt'], 'paths': ['other','files']})`,
			[]string{"three.txt"}},
		{"and the same with the paths reversed",
			`query('first_found', {'files': ['one.txt','three.txt'], 'paths': ['files','other']})`,
			[]string{"one.txt"}},
		// Real's "magic extra splitting", deprecated but alive in
		// 2.21.4: a files string splits on , and ;, a paths string also
		// on :.
		{"a files string splits on a comma", `query('first_found', 'nope.txt,two.txt')`, []string{"two.txt"}},
		{"a paths string splits on a colon", `query('first_found', 'three.txt', paths='nowhere:other')`, []string{"three.txt"}},
		// unfrackpath(follow=False): the link, not its target.
		{"a symlink is reported as itself", `query('first_found', 'link.txt')`, []string{"link.txt"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := base(t, tc.expr); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("Eval(%s) = %v, real gives %v", tc.expr, got, tc.want)
			}
		})
	}

	// Always exactly one, never several.
	if got := base(t, `query('first_found', 'one.txt', 'two.txt')`); len(got) != 1 {
		t.Errorf("got %v, real returns exactly one result", got)
	}

	// unfrackpath also NORMALISES: real returns
	// <root>/files/one.txt for a term that walks up and back down.
	// Comparing basenames cannot see this, so it compares the whole
	// path.
	got, err := e.Eval(`query('first_found', 'files/../files/one.txt')`, vars)
	if err != nil {
		t.Fatalf("Eval: %v", err)
	}
	want := []any{filepath.Join(root, "files", "one.txt")}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("a term walking up = %#v, real normalises it to %#v", got, want)
	}
}

func TestLookupFirstFoundNotFound(t *testing.T) {
	_, vars := firstFoundTree(t)
	e := New()

	// Real: "No file was found when using first_found."
	if _, err := e.Eval(`query('first_found', 'nope1', 'nope2')`, vars); err == nil {
		t.Error("nothing found and skip unset must fail")
	} else if !strings.Contains(err.Error(), "No file was found when using first_found.") {
		t.Errorf("not found: %v", err)
	}

	for _, expr := range []string{
		`query('first_found', 'nope1', 'nope2', skip=true)`,
		// A dict term carrying skip sets it for the WHOLE call.
		`query('first_found', {'files': ['nope.txt'], 'skip': true})`,
	} {
		got, err := e.Eval(expr, vars)
		if err != nil {
			t.Fatalf("Eval(%s): %v", expr, err)
		}
		if !reflect.DeepEqual(got, []any{}) {
			t.Errorf("Eval(%s) = %#v, real gives [] when skip is set", expr, got)
		}
	}

	// Real: Invalid term supplied. A string, dict or list is required,
	// not 'int').   -- stray parenthesis included.
	if _, err := e.Eval(`query('first_found', 7)`, vars); err == nil {
		t.Error("a term that is neither string, dict nor list must fail")
	} else if !strings.Contains(err.Error(), "Invalid term supplied. A string, dict or list is required, not 'int').") {
		t.Errorf("bad term: %v", err)
	}
}
