// Package template implements Ansible's templating semantics on top of a
// Jinja2-compatible engine (github.com/nikolalohinski/gonja/v2): string
// interpolation with {{ }}, control structures with {% %}, Ansible's own
// filter and test library on top of the Jinja2 built-ins, and Ansible's
// rule that a value which is a SINGLE {{ expr }} with nothing else around
// it renders to the expression's native type (list, dict, int, bool...),
// not a string.
package template

import (
	"fmt"
	"strings"

	"github.com/nikolalohinski/gonja/v2/builtins"
	"github.com/nikolalohinski/gonja/v2/config"
	"github.com/nikolalohinski/gonja/v2/exec"
	"github.com/nikolalohinski/gonja/v2/loaders"
	"github.com/nikolalohinski/gonja/v2/nodes"
	"github.com/nikolalohinski/gonja/v2/parser"
	"github.com/nikolalohinski/gonja/v2/tokens"
)

// Engine renders Ansible-flavored Jinja2 templates and expressions.
type Engine struct {
	// OnWarning, when set, receives warnings a render would otherwise
	// have nowhere to go — currently only a lookup called with
	// errors=warn, which real Ansible reports through its own Display
	// layer. Left nil, such a lookup degrades to errors=ignore.
	OnWarning func(msg string)

	cfg     *config.Config
	env     *exec.Environment
	loader  loaders.Loader
	lookups map[string]lookupFunc
}

// New returns an Engine with Jinja2's built-in filters/tests plus
// Ansible's filter and test library.
func New() *Engine {
	cfg := config.New()
	cfg.KeepTrailingNewline = true

	filters := exec.NewFilterSet(map[string]exec.FilterFunction{}).Update(builtins.Filters)
	registerFilters(filters)

	tests := exec.NewTestSet(map[string]exec.TestFunction{}).Update(builtins.Tests)
	registerTests(tests)

	// "omit" is Ansible's own sentinel global — {{ x | default(omit) }}
	// evaluates to Omit when x is undefined, and RenderValue drops the
	// containing map key or list item for it (see omit.go).
	globals := exec.NewContext(map[string]any{"omit": Omit})

	env := &exec.Environment{
		Context:           exec.EmptyContext().Update(builtins.GlobalFunctions).Update(builtins.GlobalVariables).Update(globals),
		Filters:           filters,
		Tests:             tests,
		ControlStructures: builtins.ControlStructures,
		Methods:           builtins.Methods,
	}

	lookups := map[string]lookupFunc{}
	registerLookups(lookups)

	return &Engine{
		cfg:     cfg,
		env:     env,
		loader:  loaders.MustNewMemoryLoader(map[string]string{}),
		lookups: lookups,
	}
}

// IsTemplate reports whether s contains any Jinja2 delimiter ({{, {%, or
// {#) and therefore needs rendering at all.
func IsTemplate(s string) bool {
	return strings.Contains(s, "{{") || strings.Contains(s, "{%") || strings.Contains(s, "{#")
}

// wholeExpression reports whether s is, once surrounding whitespace is
// trimmed, exactly one {{ ... }} block and nothing else — the case where
// Ansible preserves the native type instead of stringifying.
func wholeExpression(s string) (expr string, ok bool) {
	t := strings.TrimSpace(s)
	if !strings.HasPrefix(t, "{{") || !strings.HasSuffix(t, "}}") {
		return "", false
	}
	inner := t[2 : len(t)-2]
	if strings.Contains(inner, "}}") {
		return "", false // more than one block
	}
	return strings.TrimSpace(inner), true
}

// Render renders a full template string (text mixed with {{ }} / {% %})
// to its string form.
func (e *Engine) Render(src string, data map[string]any) (string, error) {
	tpl, err := exec.NewTemplate("/template", e.cfg, loaders.MustNewMemoryLoader(map[string]string{"/template": src}), e.env)
	if err != nil {
		return "", fmt.Errorf("template: parsing: %w", err)
	}
	out, err := tpl.ExecuteToString(e.callContext(data))
	if err != nil {
		return "", fmt.Errorf("template: rendering: %w", err)
	}
	return out, nil
}

// Eval evaluates a single Jinja2 expression (no {{ }} wrapper — e.g. the
// raw text of an Ansible `when:` condition, or the inside of a
// wholeExpression string) and returns its native Go value.
func (e *Engine) Eval(exprSrc string, data map[string]any) (any, error) {
	val, err := e.evalValue(exprSrc, data)
	if err != nil {
		return nil, err
	}
	return val.ToGoSimpleType(false), nil
}

// EvalBool evaluates exprSrc and applies Ansible/Jinja2 truthiness — the
// semantics of a `when:` condition.
func (e *Engine) EvalBool(exprSrc string, data map[string]any) (bool, error) {
	val, err := e.evalValue(exprSrc, data)
	if err != nil {
		return false, err
	}
	return val.IsTrue(), nil
}

func (e *Engine) evalValue(exprSrc string, data map[string]any) (*exec.Value, error) {
	// ParseExpression alone expects the stream positioned just past a
	// VariableBegin token (the state Parse() is in while walking a real
	// {{ }} block) — wrap the source the same way and use the exported
	// ParseExpressionNode, which consumes the {{ / }} delimiters for us.
	stream := tokens.Lex("{{ "+exprSrc+" }}", e.cfg)
	p := parser.NewParser("<expr>", stream, e.cfg, e.loader, e.env.ControlStructures)
	node, err := p.ParseExpressionNode()
	if err != nil {
		return nil, fmt.Errorf("template: parsing expression %q: %w", exprSrc, err)
	}
	output, ok := node.(*nodes.Output)
	if !ok {
		return nil, fmt.Errorf("template: parsing expression %q: unexpected node type %T", exprSrc, node)
	}
	ctx := e.env.Context.Inherit().Update(e.callContext(data))
	evaluator := &exec.Evaluator{
		Config: e.cfg,
		Environment: &exec.Environment{
			Context:           ctx,
			Filters:           e.env.Filters,
			Tests:             e.env.Tests,
			ControlStructures: e.env.ControlStructures,
			Methods:           e.env.Methods,
		},
		Loader: e.loader,
	}
	val := evaluator.Eval(output.Expression)
	if val.IsError() {
		return nil, fmt.Errorf("template: evaluating expression %q: %s", exprSrc, val.Error())
	}
	return val, nil
}

// RenderValue applies Ansible's templating rule to an arbitrary decoded
// YAML value: strings are templated (with the whole-expression rule
// preserving native types), and lists/maps are walked recursively so a
// nested `{{ }}` anywhere in a task's arguments is resolved. Non-string
// scalars pass through unchanged.
//
// A value that renders to Omit (see omit.go — typically reached via
// `default(omit)`) is dropped entirely: from a map, the whole key
// disappears; from a list, the item disappears — matching real Ansible's
// own "Omit values remaining in template results will be automatically
// dropped during template finalization." A bare top-level result of
// exactly Omit, with no containing map or list to drop it from, is an
// error, matching real Ansible's own AnsibleValueOmittedError.
func (e *Engine) RenderValue(raw any, data map[string]any) (any, error) {
	out, err := e.renderValue(raw, data)
	if err != nil {
		return nil, err
	}
	if IsOmit(out) {
		return nil, fmt.Errorf("template: omit has no containing list or dict entry to omit it from")
	}
	return out, nil
}

func (e *Engine) renderValue(raw any, data map[string]any) (any, error) {
	switch v := raw.(type) {
	case string:
		if !IsTemplate(v) {
			return v, nil
		}
		if expr, ok := wholeExpression(v); ok {
			return e.Eval(expr, data)
		}
		return e.Render(v, data)
	case map[string]any:
		out := make(map[string]any, len(v))
		for k, item := range v {
			rendered, err := e.renderValue(item, data)
			if err != nil {
				return nil, err
			}
			if IsOmit(rendered) {
				continue
			}
			out[k] = rendered
		}
		return out, nil
	case []any:
		out := make([]any, 0, len(v))
		for _, item := range v {
			rendered, err := e.renderValue(item, data)
			if err != nil {
				return nil, err
			}
			if IsOmit(rendered) {
				continue
			}
			out = append(out, rendered)
		}
		return out, nil
	default:
		return raw, nil
	}
}
