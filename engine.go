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
	cfg    *config.Config
	env    *exec.Environment
	loader loaders.Loader
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

	env := &exec.Environment{
		Context:           exec.EmptyContext().Update(builtins.GlobalFunctions).Update(builtins.GlobalVariables),
		Filters:           filters,
		Tests:             tests,
		ControlStructures: builtins.ControlStructures,
		Methods:           builtins.Methods,
	}

	return &Engine{
		cfg:    cfg,
		env:    env,
		loader: loaders.MustNewMemoryLoader(map[string]string{}),
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
	out, err := tpl.ExecuteToString(exec.NewContext(data))
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
	ctx := e.env.Context.Inherit().Update(exec.NewContext(data))
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
func (e *Engine) RenderValue(raw any, data map[string]any) (any, error) {
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
			rendered, err := e.RenderValue(item, data)
			if err != nil {
				return nil, err
			}
			out[k] = rendered
		}
		return out, nil
	case []any:
		out := make([]any, len(v))
		for i, item := range v {
			rendered, err := e.RenderValue(item, data)
			if err != nil {
				return nil, err
			}
			out[i] = rendered
		}
		return out, nil
	default:
		return raw, nil
	}
}
