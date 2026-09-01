package template

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/nikolalohinski/gonja/v2/exec"
)

// registerTests adds Ansible's test library (result-status tests plus
// `version`) on top of Jinja2's built-in tests (already present in the
// set passed in).
func registerTests(tests *exec.TestSet) {
	must := func(name string, fn exec.TestFunction) {
		if err := tests.Register(name, fn); err != nil {
			panic(fmt.Sprintf("template: registering test %q: %v", name, err))
		}
	}

	must("changed", testResultFlag("changed"))
	must("success", testResultStatus(true))
	must("succeeded", testResultStatus(true))
	must("failed", testResultStatus(false))
	must("failure", testResultStatus(false))
	must("skipped", testResultFlag("skipped"))
	must("version", testVersion)
}

// testResultFlag builds a test for a registered task result's boolean
// flag (e.g. `is changed`, `is skipped`): true only if the flag is
// present and truthy.
func testResultFlag(key string) exec.TestFunction {
	return func(e *exec.Evaluator, in *exec.Value, params *exec.VarArgs) (bool, error) {
		m, ok := in.ToGoSimpleType(false).(map[string]any)
		if !ok {
			return false, nil
		}
		v, ok := m[key]
		if !ok {
			return false, nil
		}
		b, _ := v.(bool)
		return b, nil
	}
}

// testResultStatus builds `is success`/`is failed`: a task result is
// failed if its "failed" key is true OR its "rc" key is a nonzero int;
// success is simply the negation.
func testResultStatus(wantSuccess bool) exec.TestFunction {
	return func(e *exec.Evaluator, in *exec.Value, params *exec.VarArgs) (bool, error) {
		m, ok := in.ToGoSimpleType(false).(map[string]any)
		if !ok {
			return !wantSuccess, nil
		}
		failed := false
		if v, ok := m["failed"].(bool); ok {
			failed = v
		}
		if rc, ok := m["rc"]; ok {
			if n, ok := toInt(rc); ok && n != 0 {
				failed = true
			}
		}
		return failed != wantSuccess, nil
	}
}

func toInt(v any) (int, bool) {
	switch n := v.(type) {
	case int:
		return n, true
	case int64:
		return int(n), true
	case float64:
		return int(n), true
	default:
		return 0, false
	}
}

// testVersion implements Ansible's `is version(other, operator)` test:
// `{{ ansible_facts.distribution_version is version('20.04', '>=') }}`.
// Versions are compared component-wise as dotted integers (a pragmatic
// subset of PEP 440 that covers the versions real playbooks compare).
func testVersion(e *exec.Evaluator, in *exec.Value, params *exec.VarArgs) (bool, error) {
	if len(params.Args) < 1 {
		return false, fmt.Errorf("version: expected at least 1 argument (the version to compare against)")
	}

	// gonja parses a test call's comma-separated arguments as a single
	// list value (`version('2.9', '>=')` -> one ValuesList arg), unlike
	// filter calls which get proper multiple positional Args. Accept
	// both shapes so `is version(other, op)` and `is version(other)
	// operator` (kwarg) both work.
	var other, op string
	op = "=="
	first := params.Args[0]
	if first.IsList() {
		parts, _ := first.ToGoSimpleType(false).([]any)
		if len(parts) < 1 {
			return false, fmt.Errorf("version: expected at least 1 argument (the version to compare against)")
		}
		other = fmt.Sprintf("%v", parts[0])
		if len(parts) > 1 {
			op = fmt.Sprintf("%v", parts[1])
		}
	} else {
		other = first.String()
		if len(params.Args) > 1 {
			op = params.Args[1].String()
		} else if v, ok := params.KwArgs["operator"]; ok {
			op = v.String()
		}
	}

	cmp := compareVersions(in.String(), other)
	switch op {
	case "==", "=", "eq":
		return cmp == 0, nil
	case "!=", "<>", "ne":
		return cmp != 0, nil
	case "<", "lt":
		return cmp < 0, nil
	case "<=", "le":
		return cmp <= 0, nil
	case ">", "gt":
		return cmp > 0, nil
	case ">=", "ge":
		return cmp >= 0, nil
	default:
		return false, fmt.Errorf("version: unknown operator %q", op)
	}
}

func compareVersions(a, b string) int {
	as := strings.Split(a, ".")
	bs := strings.Split(b, ".")
	for i := 0; i < len(as) || i < len(bs); i++ {
		var av, bv int
		if i < len(as) {
			av = versionPart(as[i])
		}
		if i < len(bs) {
			bv = versionPart(bs[i])
		}
		if av != bv {
			if av < bv {
				return -1
			}
			return 1
		}
	}
	return 0
}

// versionPart extracts the leading integer of a version component,
// tolerating suffixes like "1rc1" -> 1.
func versionPart(s string) int {
	i := 0
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	n, _ := strconv.Atoi(s[:i])
	return n
}
