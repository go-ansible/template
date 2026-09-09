package template

import (
	"fmt"
	"sort"
	"strings"

	"github.com/nikolalohinski/gonja/v2/exec"
)

// lookupFunc is one lookup plugin. It mirrors real Ansible's own
// LookupBase.run(terms, variables, **kwargs) signature: terms are the
// positional arguments after the plugin name, variables is the full
// variable context in scope where the lookup was called, kwargs are the
// remaining keyword arguments (with the universal "wantlist"/"errors"
// already consumed by the dispatcher). A plugin always returns a LIST,
// even for a single result — real Ansible's own contract, which the
// dispatcher then unwraps per wantlist.
type lookupFunc func(terms []any, variables map[string]any, kwargs map[string]any) ([]any, error)

// registerLookups installs the built-in lookup plugins, mirroring how
// registerFilters/registerTests populate their own sets.
func registerLookups(lookups map[string]lookupFunc) {
	lookups["env"] = lookupEnv
	lookups["pipe"] = lookupPipe
	lookups["file"] = lookupFile
}

// normalizeLookupName strips a known collection prefix, so
// lookup('ansible.builtin.env', ...) resolves the same as lookup('env',
// ...) — the same flat-namespace narrowing modules.NormalizeName already
// applies to module names elsewhere in this port.
func normalizeLookupName(name string) string {
	for _, prefix := range []string{"ansible.builtin.", "ansible.legacy."} {
		if strings.HasPrefix(name, prefix) {
			return strings.TrimPrefix(name, prefix)
		}
	}
	return name
}

// lookupGlobals builds this call's lookup/query/q global functions. They
// are built per render/eval call rather than once in New() because a
// lookup plugin receives the CURRENT variable context as its `variables`
// argument (real Ansible passes templar.available_variables), and gonja's
// own exec.Context exposes no way to enumerate what is in scope at call
// time — only keyed Get/Has. Capturing the caller's own data map directly
// sidesteps that entirely. Neither the caller's map nor the Engine's
// shared environment context is mutated: both are only read.
func (e *Engine) lookupGlobals(variables map[string]any) *exec.Context {
	return exec.NewContext(map[string]any{
		"lookup": func(_ *exec.Evaluator, params *exec.VarArgs) *exec.Value {
			return e.invokeLookup(variables, params, false)
		},
		// query and its alias q are real Ansible's own "same thing, but
		// always give me the raw list" wrappers around lookup.
		"query": func(_ *exec.Evaluator, params *exec.VarArgs) *exec.Value {
			return e.invokeLookup(variables, params, true)
		},
		"q": func(_ *exec.Evaluator, params *exec.VarArgs) *exec.Value {
			return e.invokeLookup(variables, params, true)
		},
	})
}

// callContext returns a fresh context carrying this call's own variables
// plus its lookup globals. It allocates its own map, so merging never
// writes into the caller's data map or the Engine's shared context.
func (e *Engine) callContext(data map[string]any) *exec.Context {
	ctx := exec.NewContext(map[string]any{})
	ctx.Update(exec.NewContext(data))
	ctx.Update(e.lookupGlobals(data))
	return ctx
}

// invokeLookup ports real Ansible's own _invoke_lookup dispatch
// (ansible/_internal/_templating/_jinja_plugins.py): resolve the named
// plugin, hand it the terms plus the live variable context, then shape
// the result per the two universal keyword arguments every lookup call
// accepts regardless of plugin — wantlist and errors.
func (e *Engine) invokeLookup(variables map[string]any, params *exec.VarArgs, forceWantlist bool) *exec.Value {
	if len(params.Args) < 1 {
		return exec.ValueError(fmt.Errorf("lookup: expected a plugin name as the first argument"))
	}
	name := normalizeLookupName(params.Args[0].String())
	fn, ok := e.lookups[name]
	if !ok {
		names := make([]string, 0, len(e.lookups))
		for n := range e.lookups {
			names = append(names, n)
		}
		sort.Strings(names)
		return exec.ValueError(fmt.Errorf("lookup: no plugin named %q, have %s", name, strings.Join(names, ", ")))
	}

	terms := make([]any, 0, len(params.Args)-1)
	for _, arg := range params.Args[1:] {
		terms = append(terms, arg.ToGoSimpleType(false))
	}

	wantlist := forceWantlist
	errorsMode := "strict"
	kwargs := make(map[string]any, len(params.KwArgs))
	for k, v := range params.KwArgs {
		switch k {
		case "wantlist":
			// query/q force it on; an explicit wantlist=False cannot turn
			// them back into lookup, matching real Ansible's own _query.
			if !forceWantlist {
				wantlist = v.IsTrue()
			}
		case "errors":
			errorsMode = v.String()
		default:
			kwargs[k] = v.ToGoSimpleType(false)
		}
	}

	result, err := fn(terms, variables, kwargs)
	if err != nil {
		switch errorsMode {
		case "ignore":
		case "warn":
			// Real Ansible warns through its own Display layer, which this
			// package has none of; OnWarning is the caller-supplied
			// equivalent (see Engine.OnWarning). With no hook installed,
			// warn degrades to ignore.
			if e.OnWarning != nil {
				e.OnWarning(fmt.Sprintf("An error occurred while running the lookup plugin %q: %v", name, err))
			}
		default: // strict
			return exec.ValueError(fmt.Errorf("lookup %q: %w", name, err))
		}
		if wantlist {
			return exec.AsValue([]any{})
		}
		return exec.AsValue(nil)
	}

	// Real Ansible's own result shaping, reproduced exactly: with
	// wantlist the raw list is returned; without it a single element is
	// unwrapped, several all-string elements are joined with a bare
	// comma, and anything else is left as a list.
	if !wantlist && len(result) > 0 {
		if len(result) == 1 {
			return exec.AsValue(result[0])
		}
		parts := make([]string, 0, len(result))
		for _, r := range result {
			s, ok := r.(string)
			if !ok {
				return exec.AsValue(result)
			}
			parts = append(parts, s)
		}
		return exec.AsValue(strings.Join(parts, ","))
	}
	return exec.AsValue(result)
}

// kwargBool reads a boolean lookup option, tolerating the string spellings
// a YAML/Jinja caller can produce as well as a real bool.
func kwargBool(kwargs map[string]any, name string, def bool) bool {
	v, ok := kwargs[name]
	if !ok {
		return def
	}
	switch t := v.(type) {
	case bool:
		return t
	case string:
		switch strings.ToLower(strings.TrimSpace(t)) {
		case "yes", "true", "on", "1":
			return true
		case "no", "false", "off", "0", "":
			return false
		}
	}
	return def
}
