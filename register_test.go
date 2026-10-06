package template

import (
	"strings"
	"testing"

	"github.com/nikolalohinski/gonja/v2/exec"
)

func TestRegisterFilter(t *testing.T) {
	e := New()
	shout := func(_ *exec.Evaluator, in *exec.Value, params *exec.VarArgs) *exec.Value {
		return exec.AsValue(strings.ToUpper(in.String()) + "!")
	}
	if err := e.RegisterFilter("shout", shout); err != nil {
		t.Fatal(err)
	}
	out, err := e.Render("{{ word | shout }}", map[string]any{"word": "hello"})
	if err != nil {
		t.Fatal(err)
	}
	if out != "HELLO!" {
		t.Fatalf("got %q", out)
	}
	// A second engine is untouched: registration is per-engine, not global.
	other := New()
	if _, err := other.Render("{{ word | shout }}", map[string]any{"word": "hello"}); err == nil {
		t.Fatal("custom filter leaked into another engine")
	}
}

func TestRegisterFilterDuplicate(t *testing.T) {
	e := New()
	fn := func(_ *exec.Evaluator, in *exec.Value, params *exec.VarArgs) *exec.Value {
		return in
	}
	if err := e.RegisterFilter("default", fn); err == nil {
		t.Fatal("registering an existing filter name must fail, not replace it")
	}
	// The existing null-aware default() keeps its behaviour.
	if got, err := e.Render("{{ missing | default('x') }}", map[string]any{}); err != nil || got != "x" {
		t.Fatalf("default after failed registration: got %q, err %v", got, err)
	}
}
