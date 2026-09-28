package template

import (
	"strings"
	"testing"
)

// TestNullIsNotUndefined pins the distinction gonja conflates. Every
// expected value was measured against ansible-core 2.21.4 with
// `nullvar: null` and `d: {a: 1, n: null}` in scope.
func TestNullIsNotUndefined(t *testing.T) {
	e := New()
	vars := map[string]any{
		"nullvar": nil,
		"d":       map[string]any{"a": 1, "n": nil},
	}

	for _, tc := range []struct {
		name string
		expr string
		want any
	}{
		// The heart of it: a variable set to null EXISTS.
		{"a null is defined", `nullvar is defined`, true},
		{"a null is not undefined", `nullvar is undefined`, false},
		{"a null inside a dict is defined", `d.n is defined`, true},
		// ...and an absent one still is not.
		{"an absent name is not defined", `no_such_name is defined`, false},
		{"an absent name is undefined", `no_such_name is undefined`, true},
		{"an absent key is not defined", `d.zz is defined`, false},

		// default() substitutes for an UNDEFINED only. Real renders
		// `{{ nullvar | default('X') }}` as empty.
		{"default does not fire on a null", `nullvar | default('X')`, nil},
		{"default fires on an absent name", `no_such_name | default('X')`, "X"},
		{"default fires on an absent key", `d.zz | default('X')`, "X"},

		// ...unless boolean mode widens it to any falsy value, which
		// is the documented way to catch a null.
		{"boolean mode catches a null", `nullvar | default('X', true)`, "X"},
		{"boolean mode catches an empty string", `'' | default('X', true)`, "X"},
		{"boolean mode as a keyword", `nullvar | default('X', boolean=true)`, "X"},
		{"boolean mode leaves a real value alone", `1 | default('X', true)`, 1},

		// `is none` was already right on both sides, and stays right.
		{"a null is none", `nullvar is none`, true},
		{"a set value is not none", `d.a is none`, false},

		// The consequence a playbook actually meets.
		{"a conditional sees the null", `'yes' if nullvar is defined else 'no'`, "yes"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := e.Eval(tc.expr, vars)
			if err != nil {
				t.Fatalf("Eval(%s): %v", tc.expr, err)
			}
			if got != tc.want {
				t.Errorf("Eval(%s) = %#v, real gives %#v", tc.expr, got, tc.want)
			}
		})
	}
}

// TestDefaultChainedOntoLength pins a measured CONSEQUENCE: because
// default() no longer fires on a null, a filter downstream of it
// receives the null. Real fails there --
//
//	object of type 'NoneType' has no len()
//
// -- where this port used to hand `length` an empty string and answer
// 0. The failure is real's, and the point of the case is that the null
// now reaches the next filter at all.
func TestDefaultChainedOntoLength(t *testing.T) {
	e := New()
	got, err := e.Eval(`nullvar | default('')`, map[string]any{"nullvar": nil})
	if err != nil {
		t.Fatalf("Eval: %v", err)
	}
	if got != nil {
		t.Errorf("nullvar | default('') = %#v, real leaves the null in place", got)
	}
}

// TestMandatoryRefusesUndefinedNotNull is the same distinction on the
// filter that exists to enforce it, which this port had exactly
// inverted.
func TestMandatoryRefusesUndefinedNotNull(t *testing.T) {
	e := New()
	vars := map[string]any{"nullvar": nil, "d": map[string]any{"a": 1}}

	if got, err := e.Eval(`nullvar | mandatory`, vars); err != nil {
		t.Errorf("mandatory over a null: %v -- real passes it through", err)
	} else if got != nil {
		t.Errorf("mandatory over a null = %#v, want nil", got)
	}

	for _, tc := range []struct{ expr, want string }{
		{`no_such | mandatory`, "Mandatory variable 'no_such' not defined."},
		{`d.absent | mandatory`, "Mandatory variable 'absent' not defined."},
	} {
		_, err := e.Eval(tc.expr, vars)
		if err == nil {
			t.Errorf("%s: got nil error, want one", tc.expr)
			continue
		}
		if !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s:\n got %v\nwant %q (real names the variable)", tc.expr, err, tc.want)
		}
	}
}
