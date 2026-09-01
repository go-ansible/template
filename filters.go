package template

import (
	"crypto/md5"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path"
	"strings"

	"regexp"

	pcre "github.com/go-regexp/engine"
	"github.com/nikolalohinski/gonja/v2/exec"
	"gopkg.in/yaml.v3"
)

// registerFilters adds Ansible's filter library on top of Jinja2's
// built-ins (already present in the set passed in).
func registerFilters(filters *exec.FilterSet) {
	must := func(name string, fn exec.FilterFunction) {
		if err := filters.Register(name, fn); err != nil {
			panic(fmt.Sprintf("template: registering filter %q: %v", name, err))
		}
	}

	must("to_json", filterToJSON(false))
	must("to_nice_json", filterToJSON(true))
	must("from_json", filterFromJSON)
	must("to_yaml", filterToYAML)
	must("to_nice_yaml", filterToYAML)
	must("from_yaml", filterFromYAML)

	must("regex_replace", filterRegexReplace)
	must("regex_search", filterRegexSearch)
	must("regex_findall", filterRegexFindall)
	must("regex_escape", filterRegexEscape)

	must("bool", filterBool)
	must("mandatory", filterMandatory)
	must("ternary", filterTernary)
	must("combine", filterCombine)
	must("dict2items", filterDict2Items)
	must("items2dict", filterItems2Dict)
	must("type_debug", filterTypeDebug)
	must("quote", filterQuote)
	must("basename", filterBasename)
	must("dirname", filterDirname)

	must("b64encode", filterB64Encode)
	must("b64decode", filterB64Decode)
	must("md5", filterHash(func(b []byte) string { s := md5.Sum(b); return hex.EncodeToString(s[:]) }))
	must("sha1", filterHash(func(b []byte) string { s := sha1.Sum(b); return hex.EncodeToString(s[:]) }))
	must("hash", filterHash(func(b []byte) string { s := sha256.Sum256(b); return hex.EncodeToString(s[:]) }))
}

func filterToJSON(indent bool) exec.FilterFunction {
	return func(e *exec.Evaluator, in *exec.Value, params *exec.VarArgs) *exec.Value {
		var (
			data []byte
			err  error
		)
		if indent {
			data, err = json.MarshalIndent(in.ToGoSimpleType(false), "", "    ")
		} else {
			data, err = json.Marshal(in.ToGoSimpleType(false))
		}
		if err != nil {
			return exec.ValueError(fmt.Errorf("to_json: %w", err))
		}
		return exec.AsValue(string(data))
	}
}

func filterFromJSON(e *exec.Evaluator, in *exec.Value, params *exec.VarArgs) *exec.Value {
	var out any
	if err := json.Unmarshal([]byte(in.String()), &out); err != nil {
		return exec.ValueError(fmt.Errorf("from_json: %w", err))
	}
	return exec.AsValue(out)
}

func filterToYAML(e *exec.Evaluator, in *exec.Value, params *exec.VarArgs) *exec.Value {
	data, err := yaml.Marshal(in.ToGoSimpleType(false))
	if err != nil {
		return exec.ValueError(fmt.Errorf("to_yaml: %w", err))
	}
	return exec.AsValue(strings.TrimRight(string(data), "\n"))
}

func filterFromYAML(e *exec.Evaluator, in *exec.Value, params *exec.VarArgs) *exec.Value {
	var out any
	if err := yaml.Unmarshal([]byte(in.String()), &out); err != nil {
		return exec.ValueError(fmt.Errorf("from_yaml: %w", err))
	}
	return exec.AsValue(normalizeYAML(out))
}

// normalizeYAML converts yaml.v3's map[string]interface{} tree (already
// string-keyed) into plain any so gonja's Value machinery, which expects
// Go-native maps, walks it without surprises.
func normalizeYAML(v any) any {
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, val := range t {
			out[k] = normalizeYAML(val)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, val := range t {
			out[i] = normalizeYAML(val)
		}
		return out
	default:
		return v
	}
}

// pyReplacement converts Python re.sub-style backreferences (\1, \g<1>)
// in a regex_replace replacement string into Go's regexp $1 syntax, and
// escapes any literal '$' so it survives ReplaceAllString unchanged.
func pyReplacement(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		switch {
		case s[i] == '$':
			b.WriteString("$$")
		case s[i] == '\\' && i+1 < len(s) && s[i+1] == 'g' && i+2 < len(s) && s[i+2] == '<':
			end := strings.IndexByte(s[i+3:], '>')
			if end >= 0 {
				b.WriteString("${" + s[i+3:i+3+end] + "}")
				i += 3 + end
				continue
			}
			b.WriteByte(s[i])
		case s[i] == '\\' && i+1 < len(s) && s[i+1] >= '0' && s[i+1] <= '9':
			b.WriteByte('$')
			b.WriteByte(s[i+1])
			i++
		default:
			b.WriteByte(s[i])
		}
	}
	return b.String()
}

func filterRegexReplace(e *exec.Evaluator, in *exec.Value, params *exec.VarArgs) *exec.Value {
	p := params.Expect(2, nil)
	if p.IsError() {
		return exec.ValueError(fmt.Errorf("%s", p.Error()))
	}
	pattern := p.Args[0].String()
	replacement := p.Args[1].String()
	re, err := pcre.Compile(pattern)
	if err != nil {
		return exec.ValueError(fmt.Errorf("regex_replace: %w", err))
	}
	return exec.AsValue(re.ReplaceAllString(in.String(), pyReplacement(replacement)))
}

func filterRegexSearch(e *exec.Evaluator, in *exec.Value, params *exec.VarArgs) *exec.Value {
	if len(params.Args) < 1 {
		return exec.ValueError(fmt.Errorf("regex_search: expected at least 1 argument"))
	}
	re, err := pcre.Compile(params.Args[0].String())
	if err != nil {
		return exec.ValueError(fmt.Errorf("regex_search: %w", err))
	}
	m := re.FindString(in.String())
	if m == "" && !re.MatchString(in.String()) {
		return exec.AsValue(nil)
	}
	return exec.AsValue(m)
}

func filterRegexFindall(e *exec.Evaluator, in *exec.Value, params *exec.VarArgs) *exec.Value {
	if len(params.Args) < 1 {
		return exec.ValueError(fmt.Errorf("regex_findall: expected at least 1 argument"))
	}
	re, err := pcre.Compile(params.Args[0].String())
	if err != nil {
		return exec.ValueError(fmt.Errorf("regex_findall: %w", err))
	}
	matches := re.FindAllString(in.String(), -1)
	out := make([]any, len(matches))
	for i, m := range matches {
		out[i] = m
	}
	return exec.AsValue(out)
}

func filterRegexEscape(e *exec.Evaluator, in *exec.Value, params *exec.VarArgs) *exec.Value {
	return exec.AsValue(regexp.QuoteMeta(in.String()))
}

func filterBool(e *exec.Evaluator, in *exec.Value, params *exec.VarArgs) *exec.Value {
	if in.IsString() {
		switch strings.ToLower(strings.TrimSpace(in.String())) {
		case "yes", "true", "on", "1":
			return exec.AsValue(true)
		case "no", "false", "off", "0", "":
			return exec.AsValue(false)
		}
	}
	return exec.AsValue(in.IsTrue())
}

func filterMandatory(e *exec.Evaluator, in *exec.Value, params *exec.VarArgs) *exec.Value {
	if in.IsNil() {
		msg := "Mandatory variable not set"
		if len(params.Args) > 0 {
			msg = params.Args[0].String()
		}
		return exec.ValueError(fmt.Errorf("%s", msg))
	}
	return in
}

func filterTernary(e *exec.Evaluator, in *exec.Value, params *exec.VarArgs) *exec.Value {
	p := params.Expect(2, nil)
	if p.IsError() {
		return exec.ValueError(fmt.Errorf("%s", p.Error()))
	}
	if in.IsTrue() {
		return p.Args[0]
	}
	return p.Args[1]
}

func filterCombine(e *exec.Evaluator, in *exec.Value, params *exec.VarArgs) *exec.Value {
	recursive := params.GetKeywordArgument("recursive", exec.AsValue(false)).IsTrue()
	out := map[string]any{}
	merge := func(m map[string]any) {
		if !recursive {
			for k, v := range m {
				out[k] = v
			}
			return
		}
		for k, v := range m {
			if existing, ok := out[k].(map[string]any); ok {
				if incoming, ok := v.(map[string]any); ok {
					merged := map[string]any{}
					for kk, vv := range existing {
						merged[kk] = vv
					}
					for kk, vv := range incoming {
						merged[kk] = vv
					}
					out[k] = merged
					continue
				}
			}
			out[k] = v
		}
	}
	if m, ok := in.ToGoSimpleType(false).(map[string]any); ok {
		merge(m)
	}
	for _, arg := range params.Args {
		if m, ok := arg.ToGoSimpleType(false).(map[string]any); ok {
			merge(m)
		}
	}
	return exec.AsValue(out)
}

func filterDict2Items(e *exec.Evaluator, in *exec.Value, params *exec.VarArgs) *exec.Value {
	keyName, valueName := "key", "value"
	if len(params.Args) > 0 {
		keyName = params.Args[0].String()
	}
	if len(params.Args) > 1 {
		valueName = params.Args[1].String()
	}
	m, _ := in.ToGoSimpleType(false).(map[string]any)
	out := make([]any, 0, len(m))
	for k, v := range m {
		out = append(out, map[string]any{keyName: k, valueName: v})
	}
	return exec.AsValue(out)
}

func filterItems2Dict(e *exec.Evaluator, in *exec.Value, params *exec.VarArgs) *exec.Value {
	keyName := params.GetKeywordArgument("key_name", exec.AsValue("key")).String()
	valueName := params.GetKeywordArgument("value_name", exec.AsValue("value")).String()
	list, _ := in.ToGoSimpleType(false).([]any)
	out := map[string]any{}
	for _, item := range list {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		k := fmt.Sprintf("%v", m[keyName])
		out[k] = m[valueName]
	}
	return exec.AsValue(out)
}

func filterTypeDebug(e *exec.Evaluator, in *exec.Value, params *exec.VarArgs) *exec.Value {
	return exec.AsValue(fmt.Sprintf("%T", in.ToGoSimpleType(false)))
}

func filterQuote(e *exec.Evaluator, in *exec.Value, params *exec.VarArgs) *exec.Value {
	s := in.String()
	if s == "" || strings.ContainsAny(s, " \t\n'\"$`\\!*?[]{}();&|<>~#") {
		return exec.AsValue("'" + strings.ReplaceAll(s, "'", `'"'"'`) + "'")
	}
	return exec.AsValue(s)
}

func filterBasename(e *exec.Evaluator, in *exec.Value, params *exec.VarArgs) *exec.Value {
	return exec.AsValue(path.Base(in.String()))
}

func filterDirname(e *exec.Evaluator, in *exec.Value, params *exec.VarArgs) *exec.Value {
	return exec.AsValue(path.Dir(in.String()))
}

func filterB64Encode(e *exec.Evaluator, in *exec.Value, params *exec.VarArgs) *exec.Value {
	return exec.AsValue(base64.StdEncoding.EncodeToString([]byte(in.String())))
}

func filterB64Decode(e *exec.Evaluator, in *exec.Value, params *exec.VarArgs) *exec.Value {
	data, err := base64.StdEncoding.DecodeString(in.String())
	if err != nil {
		return exec.ValueError(fmt.Errorf("b64decode: %w", err))
	}
	return exec.AsValue(string(data))
}

func filterHash(sum func([]byte) string) exec.FilterFunction {
	return func(e *exec.Evaluator, in *exec.Value, params *exec.VarArgs) *exec.Value {
		return exec.AsValue(sum([]byte(in.String())))
	}
}
