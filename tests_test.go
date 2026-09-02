package template

import "testing"

func TestResultFlagEdgeCases(t *testing.T) {
	e := New()

	// Non-map input: false, no error.
	got, err := e.EvalBool(`1 is changed`, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got {
		t.Error("1 is changed: want false")
	}

	// Map missing the key entirely: false.
	got, err = e.EvalBool(`r is changed`, map[string]any{"r": map[string]any{}})
	if err != nil {
		t.Fatal(err)
	}
	if got {
		t.Error("{} is changed: want false")
	}

	// Key present but not a bool: treated as false.
	got, err = e.EvalBool(`r is changed`, map[string]any{"r": map[string]any{"changed": "yes"}})
	if err != nil {
		t.Fatal(err)
	}
	if got {
		t.Error(`{changed: "yes"} is changed: want false (not a real bool)`)
	}
}

func TestResultStatusEdgeCases(t *testing.T) {
	e := New()

	// Non-map input.
	got, err := e.EvalBool(`5 is success`, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got {
		t.Error("5 is success: want false")
	}
	got, err = e.EvalBool(`5 is failed`, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !got {
		t.Error("5 is failed: want true (non-map counts as failed)")
	}

	// failed: true directly.
	got, err = e.EvalBool(`r is failed`, map[string]any{"r": map[string]any{"failed": true}})
	if err != nil {
		t.Fatal(err)
	}
	if !got {
		t.Error("{failed: true} is failed: want true")
	}

	// rc nonzero implies failed even if "failed" is absent.
	got, err = e.EvalBool(`r is failed`, map[string]any{"r": map[string]any{"rc": 1}})
	if err != nil {
		t.Fatal(err)
	}
	if !got {
		t.Error("{rc: 1} is failed: want true")
	}

	// rc zero: not failed.
	got, err = e.EvalBool(`r is success`, map[string]any{"r": map[string]any{"rc": 0}})
	if err != nil {
		t.Fatal(err)
	}
	if !got {
		t.Error("{rc: 0} is success: want true")
	}

	// rc present but not numeric: ignored, does not force failure.
	got, err = e.EvalBool(`r is success`, map[string]any{"r": map[string]any{"rc": "not-a-number"}})
	if err != nil {
		t.Fatal(err)
	}
	if !got {
		t.Error(`{rc: "not-a-number"} is success: want true (non-numeric rc ignored)`)
	}
}

func TestToIntTypesViaRC(t *testing.T) {
	e := New()
	cases := []struct {
		name string
		rc   any
		want bool // is failed
	}{
		{"int", int(2), true},
		{"int64", int64(3), true},
		{"float64", float64(4), true},
		{"zero int", int(0), false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := e.EvalBool(`r is failed`, map[string]any{"r": map[string]any{"rc": c.rc}})
			if err != nil {
				t.Fatal(err)
			}
			if got != c.want {
				t.Errorf("rc=%v (%T) is failed = %v, want %v", c.rc, c.rc, got, c.want)
			}
		})
	}
}

func TestVersionTestEdgeCases(t *testing.T) {
	e := New()

	// Single positional arg, default operator "==".
	got, err := e.EvalBool(`'1.0' is version('1.0')`, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !got {
		t.Error("'1.0' is version('1.0'): want true (default '==' operator)")
	}

	// Unknown operator.
	if _, err := e.EvalBool(`'1.0' is version('1.0', 'weird')`, nil); err == nil {
		t.Fatal("version with an unknown operator: got nil error, want one")
	}

	// No arguments at all: gonja's `is` grammar (ParseTest) only ever
	// tries to parse a single optional variable-or-literal after the
	// test name, so the bare form below reaches testVersion with a
	// genuinely empty Args slice instead of failing to parse.
	if _, err := e.EvalBool(`'1.0' is version`, nil); err == nil {
		t.Fatal("version with no arguments: got nil error, want one")
	}

	// Empty parens parse as a single zero-length list argument (the
	// IsList() branch), a distinct empty-args case from the bare form
	// above (which never appends an Args entry at all).
	if _, err := e.EvalBool(`'1.0' is version()`, nil); err == nil {
		t.Fatal("version() with empty parens: got nil error, want one")
	}

	// Every recognized operator spelling.
	ops := []struct {
		expr string
		want bool
	}{
		{"'1.0' is version('1.0', '=')", true},
		{"'1.0' is version('1.0', 'eq')", true},
		{"'1.0' is version('1.1', 'ne')", true},
		{"'1.0' is version('1.1', '<>')", true},
		{"'1.0' is version('1.1', 'lt')", true},
		{"'1.0' is version('1.0', 'le')", true},
		{"'1.1' is version('1.0', 'gt')", true},
		{"'1.0' is version('1.0', 'ge')", true},
	}
	for _, o := range ops {
		got, err := e.EvalBool(o.expr, nil)
		if err != nil {
			t.Fatalf("%s: %v", o.expr, err)
		}
		if got != o.want {
			t.Errorf("%s = %v, want %v", o.expr, got, o.want)
		}
	}
}

func TestToInt(t *testing.T) {
	// toInt is exercised end to end via testResultStatus's rc handling,
	// but gonja normalizes Go int64 values it round-trips through
	// exec.Value back to plain int, so the int64 case is never reached
	// that way. Call it directly to cover every case it actually
	// switches on.
	cases := []struct {
		name   string
		in     any
		want   int
		wantOK bool
	}{
		{"int", int(5), 5, true},
		{"int64", int64(6), 6, true},
		{"float64", float64(7), 7, true},
		{"unsupported", "not-a-number", 0, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			n, ok := toInt(c.in)
			if n != c.want || ok != c.wantOK {
				t.Errorf("toInt(%#v) = %d,%v want %d,%v", c.in, n, ok, c.want, c.wantOK)
			}
		})
	}
}

func TestVersionPartWithSuffix(t *testing.T) {
	e := New()
	got, err := e.EvalBool(`'1rc1' is version('2', '<')`, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !got {
		t.Error("'1rc1' is version('2', '<'): want true (suffix stripped to leading digits)")
	}
}
