package template

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLookupEnv(t *testing.T) {
	e := New()
	t.Setenv("GO_ANSIBLE_LOOKUP_A", "alpha")
	t.Setenv("GO_ANSIBLE_LOOKUP_B", "beta")

	got, err := e.Eval(`lookup('env', 'GO_ANSIBLE_LOOKUP_A')`, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got != "alpha" {
		t.Errorf("lookup(env) = %#v, want %q", got, "alpha")
	}

	// A collection-qualified name resolves to the same plugin.
	got, err = e.Eval(`lookup('ansible.builtin.env', 'GO_ANSIBLE_LOOKUP_A')`, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got != "alpha" {
		t.Errorf("lookup(ansible.builtin.env) = %#v, want %q", got, "alpha")
	}

	// An unset variable yields the default option (real default: "").
	got, err = e.Eval(`lookup('env', 'GO_ANSIBLE_LOOKUP_UNSET')`, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got != "" {
		t.Errorf("lookup(env) on an unset variable = %#v, want the empty string", got)
	}

	got, err = e.Eval(`lookup('env', 'GO_ANSIBLE_LOOKUP_UNSET', default='fallback')`, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got != "fallback" {
		t.Errorf("lookup(env, default=...) = %#v, want %q", got, "fallback")
	}
}

func TestLookupResultShaping(t *testing.T) {
	e := New()
	t.Setenv("GO_ANSIBLE_LOOKUP_A", "alpha")
	t.Setenv("GO_ANSIBLE_LOOKUP_B", "beta")

	// Two all-string results, no wantlist: real Ansible joins them with a
	// bare comma rather than returning a list.
	got, err := e.Eval(`lookup('env', 'GO_ANSIBLE_LOOKUP_A', 'GO_ANSIBLE_LOOKUP_B')`, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got != "alpha,beta" {
		t.Errorf("lookup with two terms = %#v, want %q", got, "alpha,beta")
	}

	// wantlist=True keeps the raw list, and query/q are the same thing
	// with wantlist forced on.
	for _, expr := range []string{
		`lookup('env', 'GO_ANSIBLE_LOOKUP_A', 'GO_ANSIBLE_LOOKUP_B', wantlist=True)`,
		`query('env', 'GO_ANSIBLE_LOOKUP_A', 'GO_ANSIBLE_LOOKUP_B')`,
		`q('env', 'GO_ANSIBLE_LOOKUP_A', 'GO_ANSIBLE_LOOKUP_B')`,
	} {
		got, err := e.Eval(expr, nil)
		if err != nil {
			t.Fatal(err)
		}
		list, ok := got.([]any)
		if !ok || len(list) != 2 || list[0] != "alpha" || list[1] != "beta" {
			t.Errorf("%s = %#v, want the two-element list [alpha beta]", expr, got)
		}
	}

	// A single result still comes back as a real list under query.
	got, err = e.Eval(`query('env', 'GO_ANSIBLE_LOOKUP_A')`, nil)
	if err != nil {
		t.Fatal(err)
	}
	if list, ok := got.([]any); !ok || len(list) != 1 || list[0] != "alpha" {
		t.Errorf("query with one term = %#v, want a one-element list", got)
	}
}

func TestLookupPipe(t *testing.T) {
	e := New()

	got, err := e.Eval(`lookup('pipe', 'echo hi')`, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got != "hi" {
		t.Errorf("lookup(pipe) = %#v, want %q (trailing newline stripped)", got, "hi")
	}

	if _, err := e.Eval(`lookup('pipe', 'exit 3')`, nil); err == nil {
		t.Fatal("lookup(pipe) on a failing command: got nil error, want one")
	}
}

func TestLookupFile(t *testing.T) {
	e := New()
	dir := t.TempDir()
	path := filepath.Join(dir, "content.txt")
	if err := os.WriteFile(path, []byte("  hello\n\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	vars := map[string]any{"p": path}

	// Real defaults: rstrip on, lstrip off.
	got, err := e.Eval(`lookup('file', p)`, vars)
	if err != nil {
		t.Fatal(err)
	}
	if got != "  hello" {
		t.Errorf("lookup(file) = %#v, want %q", got, "  hello")
	}

	got, err = e.Eval(`lookup('file', p, rstrip=False)`, vars)
	if err != nil {
		t.Fatal(err)
	}
	if got != "  hello\n\n" {
		t.Errorf("lookup(file, rstrip=False) = %#v, want the trailing newlines kept", got)
	}

	got, err = e.Eval(`lookup('file', p, lstrip=True)`, vars)
	if err != nil {
		t.Fatal(err)
	}
	if got != "hello" {
		t.Errorf("lookup(file, lstrip=True) = %#v, want %q", got, "hello")
	}

	if _, err := e.Eval(`lookup('file', '/no/such/file/here')`, nil); err == nil {
		t.Fatal("lookup(file) on a missing file: got nil error, want one")
	}
}

func TestLookupErrorsModes(t *testing.T) {
	e := New()

	// strict (the default) surfaces the failure as a real error.
	if _, err := e.Eval(`lookup('pipe', 'exit 3')`, nil); err == nil {
		t.Fatal("errors=strict: got nil error, want one")
	}

	// ignore swallows it, yielding nil (or an empty list under wantlist).
	got, err := e.Eval(`lookup('pipe', 'exit 3', errors='ignore')`, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Errorf("errors=ignore = %#v, want nil", got)
	}

	got, err = e.Eval(`query('pipe', 'exit 3', errors='ignore')`, nil)
	if err != nil {
		t.Fatal(err)
	}
	if list, ok := got.([]any); !ok || len(list) != 0 {
		t.Errorf("errors=ignore under wantlist = %#v, want an empty list", got)
	}

	// warn reports through OnWarning and then behaves like ignore.
	var warned []string
	e.OnWarning = func(msg string) { warned = append(warned, msg) }
	got, err = e.Eval(`lookup('pipe', 'exit 3', errors='warn')`, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Errorf("errors=warn = %#v, want nil", got)
	}
	if len(warned) != 1 || !strings.Contains(warned[0], "pipe") {
		t.Errorf("errors=warn warnings = %#v, want one mentioning the plugin", warned)
	}

	// With no hook installed, warn degrades to ignore silently.
	e.OnWarning = nil
	if _, err := e.Eval(`lookup('pipe', 'exit 3', errors='warn')`, nil); err != nil {
		t.Fatalf("errors=warn with no OnWarning hook: %v", err)
	}
}

func TestLookupFileStringSpelledOptions(t *testing.T) {
	// A YAML/Jinja caller can spell a boolean option as a string; real
	// Ansible's own option handling accepts those spellings too.
	e := New()
	dir := t.TempDir()
	path := filepath.Join(dir, "content.txt")
	if err := os.WriteFile(path, []byte("  hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	vars := map[string]any{"p": path}

	got, err := e.Eval(`lookup('file', p, rstrip='no', lstrip='yes')`, vars)
	if err != nil {
		t.Fatal(err)
	}
	if got != "hello\n" {
		t.Errorf("lookup(file) with string-spelled options = %#v, want %q", got, "hello\n")
	}

	// An unrecognised spelling falls back to the option's own default.
	got, err = e.Eval(`lookup('file', p, rstrip='banana')`, vars)
	if err != nil {
		t.Fatal(err)
	}
	if got != "  hello" {
		t.Errorf("lookup(file) with an unparseable option = %#v, want the default rstrip behavior", got)
	}
}

func TestLookupMixedTypeResultStaysAList(t *testing.T) {
	// Real Ansible only comma-joins a multi-element result when every
	// element is a string; anything else comes back as the list itself.
	e := New()
	e.lookups["mixed"] = func(_ []any, _ map[string]any, _ map[string]any) ([]any, error) {
		return []any{1, "a"}, nil
	}
	got, err := e.Eval(`lookup('mixed')`, nil)
	if err != nil {
		t.Fatal(err)
	}
	list, ok := got.([]any)
	if !ok || len(list) != 2 {
		t.Fatalf("lookup with mixed-type results = %#v, want a two-element list", got)
	}

	e.lookups["empty"] = func(_ []any, _ map[string]any, _ map[string]any) ([]any, error) {
		return nil, nil
	}
	got, err = e.Eval(`lookup('empty')`, nil)
	if err != nil {
		t.Fatal(err)
	}
	if list, ok := got.([]any); !ok || len(list) != 0 {
		t.Errorf("lookup with no results = %#v, want an empty list", got)
	}
}

func TestLookupDispatchErrors(t *testing.T) {
	e := New()
	if _, err := e.Eval(`lookup('nosuchplugin', 'x')`, nil); err == nil {
		t.Fatal("unknown lookup plugin: got nil error, want one")
	}
	if _, err := e.Eval(`lookup()`, nil); err == nil {
		t.Fatal("lookup with no plugin name: got nil error, want one")
	}
}

// TestLookupReceivesVariables covers the part of the design that gonja's
// own API cannot provide on its own: a lookup plugin receives the live
// variable context of the call site (real Ansible's
// variables=templar.available_variables), which this port supplies by
// building the lookup globals per call over the caller's own data map
// rather than once at Engine construction.
func TestLookupReceivesVariables(t *testing.T) {
	e := New()
	e.lookups["echovar"] = func(terms []any, variables map[string]any, _ map[string]any) ([]any, error) {
		if len(terms) != 1 {
			return nil, fmt.Errorf("echovar: want exactly one term")
		}
		name := fmt.Sprintf("%v", terms[0])
		v, ok := variables[name]
		if !ok {
			return nil, fmt.Errorf("echovar: %q not in the variable context", name)
		}
		return []any{v}, nil
	}

	got, err := e.Eval(`lookup('echovar', 'greeting')`, map[string]any{"greeting": "bonjour"})
	if err != nil {
		t.Fatal(err)
	}
	if got != "bonjour" {
		t.Errorf("lookup reading the call's own variables = %#v, want %q", got, "bonjour")
	}

	// The same context is visible on the Render path, not just Eval.
	out, err := e.Render(`{{ lookup('echovar', 'greeting') }}`, map[string]any{"greeting": "salut"})
	if err != nil {
		t.Fatal(err)
	}
	if out != "salut" {
		t.Errorf("lookup under Render = %q, want %q", out, "salut")
	}

	// A variable absent from this call's own context is genuinely absent.
	if _, err := e.Eval(`lookup('echovar', 'greeting')`, nil); err == nil {
		t.Fatal("lookup for a variable outside the call's context: got nil error, want one")
	}
}

// TestLookupDoesNotMutateCallerData guards the design's own claim that
// building the per-call context never writes the lookup globals into the
// caller's variable map.
func TestLookupDoesNotMutateCallerData(t *testing.T) {
	e := New()
	data := map[string]any{"x": 1}
	if _, err := e.Eval(`lookup('env', 'PATH')`, data); err != nil {
		t.Fatal(err)
	}
	if len(data) != 1 {
		t.Errorf("caller data = %#v, want it untouched with just x", data)
	}
	for _, name := range []string{"lookup", "query", "q"} {
		if _, ok := data[name]; ok {
			t.Errorf("caller data gained a %q key", name)
		}
	}
}
