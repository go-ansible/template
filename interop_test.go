package template

import (
	"encoding/json"
	"os"
	"os/exec"
	"testing"
)

// ansibleBin locates a real `ansible` binary for cross-validation against
// this package's own implementation. Tests using it skip cleanly when
// none is available (e.g. in CI, which does not install Python).
func ansibleBin(t *testing.T) string {
	t.Helper()
	if p := os.Getenv("ANSIBLE_BIN"); p != "" {
		return p
	}
	p, err := exec.LookPath("ansible")
	if err != nil {
		t.Skip("ansible not found in PATH; skipping cross-validation against the reference implementation")
	}
	return p
}

// referenceEval renders expr through the real `ansible` CLI's debug
// module (`ansible localhost -m debug -a "msg={{ expr }}"`) and decodes
// its JSON result's "msg" field — the reference implementation's answer
// for what this expression evaluates to.
func referenceEval(t *testing.T, bin, expr string, extraVars map[string]any) any {
	t.Helper()
	args := []string{"localhost", "-m", "debug", "-a", "msg={{ " + expr + " }}", "-o"}
	if len(extraVars) > 0 {
		data, err := json.Marshal(extraVars)
		if err != nil {
			t.Fatal(err)
		}
		args = append(args, "-e", string(data))
	}
	out, err := exec.Command(bin, args...).Output()
	if err != nil {
		t.Fatalf("ansible -m debug: %v", err)
	}
	// -o output is one JSON-ish line: "localhost | SUCCESS => {...}"
	i := indexOfJSON(out)
	if i < 0 {
		t.Fatalf("could not find JSON in ansible output: %s", out)
	}
	var decoded struct {
		Msg any `json:"msg"`
	}
	if err := json.Unmarshal(out[i:], &decoded); err != nil {
		t.Fatalf("decoding ansible debug output: %v\n%s", err, out)
	}
	return decoded.Msg
}

func indexOfJSON(b []byte) int {
	for i, c := range b {
		if c == '{' {
			return i
		}
	}
	return -1
}

func TestInteropExpressionsAgainstReference(t *testing.T) {
	bin := ansibleBin(t)
	e := New()

	cases := []struct {
		name string
		expr string
		vars map[string]any
	}{
		{"arithmetic", "1 + 2 * 3", nil},
		{"string concat", "'a' ~ 'b'", nil},
		{"filter default", "missing | default('fallback')", nil},
		{"filter upper", "'hello' | upper", nil},
		{"var lookup", "name", map[string]any{"name": "world"}},
		{"list index", "items[1]", map[string]any{"items": []any{10, 20, 30}}},
		{"dict access", "d.a", map[string]any{"d": map[string]any{"a": 1}}},
		{"ternary", "(1 == 1) | ternary('yes', 'no')", nil},
		{"regex_replace", "'hello world' | regex_replace('world', 'there')", nil},
		{"join filter", "[1, 2, 3] | join(',')", nil},
		{"combine", "({'a': 1}) | combine({'b': 2})", nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			want := referenceEval(t, bin, c.expr, c.vars)
			got, err := e.Eval(c.expr, c.vars)
			if err != nil {
				t.Fatalf("our Eval(%q): %v", c.expr, err)
			}
			if !equalJSONish(got, want) {
				t.Errorf("Eval(%q) = %#v (%T), reference = %#v (%T)", c.expr, got, got, want, want)
			}
		})
	}
}

// equalJSONish compares values the way our native result and the
// reference's JSON-decoded result should be compared: numeric types may
// legitimately differ (int vs float64).
func equalJSONish(a, b any) bool {
	af, aok := toFloat(a)
	bf, bok := toFloat(b)
	if aok && bok {
		return af == bf
	}
	am, aok := a.(map[string]any)
	bm, bok := b.(map[string]any)
	if aok && bok {
		if len(am) != len(bm) {
			return false
		}
		for k, v := range am {
			if !equalJSONish(v, bm[k]) {
				return false
			}
		}
		return true
	}
	al, aok := a.([]any)
	bl, bok := b.([]any)
	if aok && bok {
		if len(al) != len(bl) {
			return false
		}
		for i := range al {
			if !equalJSONish(al[i], bl[i]) {
				return false
			}
		}
		return true
	}
	return a == b
}

func toFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case float64:
		return n, true
	default:
		return 0, false
	}
}
