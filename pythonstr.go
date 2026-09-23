package template

import (
	"fmt"
	"sort"
	"strings"
)

// PythonStr renders a value the way Python's str() does, which is what
// real Ansible's loop labels use: `(item=...)` on every ok/skipping
// line of a looping task.
//
// The rule that makes this more than a format verb: str() of a
// CONTAINER uses repr() of its elements. So a bare string item prints
// unquoted while the same string inside a list prints quoted —
// measured, `(item=plain)` beside `(item=['a', 1])`.
//
// Go's %v gave `map[k:v]` and `[1 <nil>]` where Python gives
// `{'k': 'v'}` and `[1, None]`, so every loop over anything but a
// scalar diverged.
func PythonStr(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return PythonRepr(v)
}

// PythonRepr renders a value the way Python's repr() does.
func PythonRepr(v any) string {
	switch t := v.(type) {
	case nil:
		return "None"
	case bool:
		if t {
			return "True"
		}
		return "False"
	case string:
		return pythonQuote(t)
	case float32:
		return pythonFloat(float64(t))
	case float64:
		return pythonFloat(t)
	case []any:
		parts := make([]string, len(t))
		for i, item := range t {
			parts[i] = PythonRepr(item)
		}
		return "[" + strings.Join(parts, ", ") + "]"
	case map[string]any:
		// Sorted, where Python preserves insertion order: this port
		// decodes YAML mappings into map[string]any, so document order
		// is gone before anything can read it. The same disclosed
		// divergence as the dict lookup's.
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		parts := make([]string, 0, len(keys))
		for _, k := range keys {
			parts = append(parts, pythonQuote(k)+": "+PythonRepr(t[k]))
		}
		return "{" + strings.Join(parts, ", ") + "}"
	default:
		return fmt.Sprintf("%v", v)
	}
}

// pythonQuote is repr() for a string: single quotes, unless the string
// contains a single quote and no double quote — then Python switches
// to double quotes rather than escaping. Measured: ["it's"] comes back
// as ["it's"], not ['it\'s'].
func pythonQuote(s string) string {
	if strings.Contains(s, "'") && !strings.Contains(s, `"`) {
		return `"` + s + `"`
	}
	return "'" + strings.ReplaceAll(s, "'", `\'`) + "'"
}
