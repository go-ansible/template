package template

import (
	"strings"
	"testing"

	"github.com/nikolalohinski/gonja/v2/exec"
)

// TestRegisterTest mirrors TestRegisterFilter (#41) for the companion
// half: Ansible extends Jinja2 with filter plugins AND test plugins, and
// this engine's test set was assembled inside New() behind the same
// unexported field, so a custom `is` test needed a fork.
func TestRegisterTest(t *testing.T) {
	e := New()
	isShouty := func(_ *exec.Evaluator, in *exec.Value, _ *exec.VarArgs) (bool, error) {
		s := in.String()
		return s != "" && s == strings.ToUpper(s), nil
	}
	if err := e.RegisterTest("shouty", isShouty); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		word string
		want string
	}{
		{"HELLO", "True"},
		{"hello", "False"},
	} {
		got, err := e.Render("{{ word is shouty }}", map[string]any{"word": tc.word})
		if err != nil {
			t.Fatal(err)
		}
		if got != tc.want {
			t.Errorf("%q is shouty -> %q, want %q", tc.word, got, tc.want)
		}
	}

	// Per-engine, not global: New builds a fresh TestSet rather than
	// sharing the package-level one, so nothing can leak between
	// engines. Asserting only the happy path above would pass just as
	// well if the set were shared.
	if _, err := New().Render("{{ word is shouty }}", map[string]any{"word": "HELLO"}); err == nil {
		t.Error("the custom test leaked into another engine")
	}
}

// An existing name is refused rather than replaced, and the built-in
// keeps working afterwards -- the half that matters, since a silent
// replacement of `version` or `changed` would break playbooks.
func TestRegisterTestDuplicate(t *testing.T) {
	e := New()
	never := func(*exec.Evaluator, *exec.Value, *exec.VarArgs) (bool, error) { return false, nil }

	if err := e.RegisterTest("changed", never); err == nil {
		t.Fatal("registering an existing test name must fail, not replace it")
	}
	got, err := e.Render("{{ r is changed }}", map[string]any{"r": map[string]any{"changed": true}})
	if err != nil {
		t.Fatal(err)
	}
	if got != "True" {
		t.Errorf("the built-in `changed` did not survive the refused registration: %q", got)
	}
}

// ⚠ gonja validates a test's SHAPE by reflection at registration, and
// this wrapper's typed signature is what keeps that error unreachable
// from Go code -- a wrong shape does not compile. The reflection is still
// the last line of defence for anything that gets past the type, so this
// pins that a correctly shaped function is accepted rather than tripping
// the validator through the exec.TestFunction conversion.
func TestRegisterTestAcceptsTheEvaluatorForm(t *testing.T) {
	e := New()
	if err := e.RegisterTest("always", func(*exec.Evaluator, *exec.Value, *exec.VarArgs) (bool, error) {
		return true, nil
	}); err != nil {
		t.Fatalf("gonja rejected the shape this package's own tests all use: %v", err)
	}
}
