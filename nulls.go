package template

import (
	"fmt"
	"regexp"

	"github.com/nikolalohinski/gonja/v2/exec"
)

// A NULL IS NOT AN UNDEFINED.
//
// gonja conflates the two: its own `defined` test is
// !(IsError() || IsNil()) and its `default` filter fires on either. Real
// Ansible, like Jinja2, keeps them apart — a variable set to null EXISTS
// and holds None. Measured against ansible-core 2.21.4, with
// `nullvar: null` in scope:
//
//	                                real     this port, before
//	nullvar is defined              True     false
//	nullvar is undefined            False    true
//	nullvar | default('X')          ''       X
//	nullvar | default('X', true)    X        X
//	nullvar | mandatory             passes   FAILED
//	no_such_name | mandatory        fails    fails, other wording
//
// The consequence is not cosmetic: `{{ x | default('fallback') }}` over
// a variable someone set to null silently substituted, and
// `when: x is defined` was false for a variable that is there.
//
// The distinction IS available on the value, which is why these can be
// fixed here rather than in gonja:
//
//	                        IsError   IsNil
//	a set value             false     false
//	a null                  false     TRUE
//	a missing attribute     TRUE      false
//	a missing name          TRUE      false
//
// What is NOT fixable here is how a null RENDERS inside a container:
// real prints [1, None, 3] and this port prints [1, , 3], because
// gonja's Value.String() does the joining and its Config exposes no
// hook for it. That is the same layer as the p_repr corpus witness,
// and it stays named rather than worked around.
func registerNullAwareOverrides(filters *exec.FilterSet, tests *exec.TestSet) {
	mustTest := func(name string, fn exec.TestFunction) {
		if err := tests.Replace(name, fn); err != nil {
			panic(fmt.Sprintf("template: replacing test %q: %v", name, err))
		}
	}
	mustFilter := func(name string, fn exec.FilterFunction) {
		if err := filters.Replace(name, fn); err != nil {
			panic(fmt.Sprintf("template: replacing filter %q: %v", name, err))
		}
	}

	// Defined means "resolved to something", null included.
	mustTest("defined", func(_ *exec.Context, in *exec.Value, _ *exec.VarArgs) (bool, error) {
		return !in.IsError(), nil
	})
	mustTest("undefined", func(_ *exec.Context, in *exec.Value, _ *exec.VarArgs) (bool, error) {
		return in.IsError(), nil
	})

	mustFilter("default", filterDefaultNullAware)
	mustFilter("d", filterDefaultNullAware)
	mustFilter("mandatory", filterMandatoryNullAware)
}

// filterDefaultNullAware is Jinja2's default(): it substitutes for an
// UNDEFINED value only. Its second argument — boolean mode, also
// spelled `boolean=true` — widens that to any falsy value, and THAT is
// what catches a null. Measured: default('X') over a null renders
// empty, default('X', true) renders X, and ” | default('X', true)
// renders X too.
func filterDefaultNullAware(e *exec.Evaluator, in *exec.Value, params *exec.VarArgs) *exec.Value {
	var fallback *exec.Value = exec.AsValue("")
	if len(params.Args) > 0 {
		fallback = params.Args[0]
	}
	boolean := false
	if len(params.Args) > 1 {
		boolean = params.Args[1].IsTrue()
	}
	if kw, ok := params.KwArgs["boolean"]; ok {
		boolean = kw.IsTrue()
	}

	if in.IsError() {
		return fallback
	}
	if boolean && !in.IsTrue() {
		return fallback
	}
	return in
}

// mandatoryNamePatterns pull the variable's name out of gonja's own
// wording, so the refusal can name it the way real's does. Two
// spellings, because gonja words a missing NAME and a missing
// ATTRIBUTE differently and real names both:
//
//	Unable to evaluate name "no_such"                   -> no_such
//	Unable to evaluate d.absent: attribute 'absent' ...  -> absent
//
// Measured: real answers "Mandatory variable 'no_such' not defined."
// and "Mandatory variable 'absent' not defined." respectively.
var mandatoryNamePatterns = []*regexp.Regexp{
	regexp.MustCompile(`Unable to evaluate name "([^"]*)"`),
	regexp.MustCompile(`attribute '([^']*)' not found`),
}

// filterMandatoryNullAware refuses an UNDEFINED value and passes a null
// through. The port had it exactly inverted: it refused a null and let
// an undefined past, so `{{ x | mandatory }}` failed for a variable
// that was set and succeeded — until the expression blew up elsewhere —
// for one that was not.
//
// Real's wording names the variable:
//
//	Mandatory variable 'no_such_name' not defined.
//
// and the name is recoverable, because gonja's error value carries it.
// Where it cannot be recovered — a missing ATTRIBUTE rather than a
// missing name — the refusal falls back to real's older wording
// without a name rather than inventing one.
func filterMandatoryNullAware(e *exec.Evaluator, in *exec.Value, params *exec.VarArgs) *exec.Value {
	if !in.IsError() {
		return in
	}
	if len(params.Args) > 0 {
		return filterFailure("mandatory", params.Args[0].String())
	}
	if kw, ok := params.KwArgs["msg"]; ok {
		return filterFailure("mandatory", kw.String())
	}
	name := ""
	if err, ok := in.Interface().(error); ok {
		for _, re := range mandatoryNamePatterns {
			if m := re.FindStringSubmatch(err.Error()); m != nil {
				name = m[1]
				break
			}
		}
	}
	if name == "" {
		return filterFailure("mandatory", "Mandatory variable not defined.")
	}
	return filterFailure("mandatory", fmt.Sprintf("Mandatory variable '%s' not defined.", name))
}

// FilterFailurePrefix is how this package words a filter plugin's own
// refusal, and it is real's wording:
//
//	The filter plugin 'ansible.builtin.mandatory' failed: <reason>
//
// It is exported because it is a CONTRACT with the playbook engine, not
// a coincidence: gonja wraps whatever a filter returns in several
// layers of its own prose ("unable to evaluate filter &{<Token...}"),
// which real has no equivalent of and never prints, and the engine
// finds the part real would have printed by looking for this prefix.
// Cutting on a boundary the other side guarantees beats a list of
// fragments to strip, which stops matching the day gonja rewords one.
//
// The same reasoning as the lookup plugin's own prefix, one layer down.
const FilterFailurePrefix = "The filter plugin '"

// filterFailure words a filter's refusal the way real words it, with
// the collection-qualified name real prints.
func filterFailure(name, reason string) *exec.Value {
	return exec.ValueError(fmt.Errorf("%sansible.builtin.%s' failed: %s", FilterFailurePrefix, name, reason))
}
