package template

import (
	"crypto/md5"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"path"
	"reflect"
	"strconv"
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

	// "unique" is deliberately not registered here: gonja's own builtin
	// already implements Jinja2's do_unique (case_sensitive/attribute
	// keyword args included), which is exactly what real Ansible's own
	// "unique" filter delegates to whenever Jinja2 provides it.
	must("union", filterUnion)
	must("intersect", filterIntersect)
	must("difference", filterDifference)
	must("symmetric_difference", filterSymmetricDifference)

	must("log", filterLog)
	must("pow", filterPow)
	must("root", filterRoot)

	must("human_readable", filterHumanReadable)
	must("human_to_bytes", filterHumanToBytes)
	must("rekey_on_member", filterRekeyOnMember)

	must("to_uuid", filterToUUID)
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

// toList reads v as a []any, or nil if it isn't one — matching this file's
// existing silent-coerce convention for wrong-typed filter input (see
// filterDict2Items/filterItems2Dict).
func toList(v *exec.Value) []any {
	l, _ := v.ToGoSimpleType(false).([]any)
	return l
}

func listContains(list []any, v any) bool {
	for _, existing := range list {
		if reflect.DeepEqual(existing, v) {
			return true
		}
	}
	return false
}

// dedupPreserveOrder deduplicates by first occurrence. Real Ansible's set
// filters build a Python set() when every element is hashable, whose
// iteration order is an implementation detail of CPython's hash table, and
// only fall back to this same order-preserving dedup when an element isn't
// hashable (e.g. a list of dicts). This port always takes that
// order-preserving path, a disclosed simplification rather than a literal
// reproduction of CPython's unspecified hash order.
func dedupPreserveOrder(vals []any) []any {
	out := make([]any, 0, len(vals))
	for _, v := range vals {
		if !listContains(out, v) {
			out = append(out, v)
		}
	}
	return out
}

func filterUnion(e *exec.Evaluator, in *exec.Value, params *exec.VarArgs) *exec.Value {
	p := params.Expect(1, nil)
	if p.IsError() {
		return exec.ValueError(fmt.Errorf("%s", p.Error()))
	}
	a, b := toList(in), toList(p.Args[0])
	return exec.AsValue(dedupPreserveOrder(append(append([]any{}, a...), b...)))
}

func filterIntersect(e *exec.Evaluator, in *exec.Value, params *exec.VarArgs) *exec.Value {
	p := params.Expect(1, nil)
	if p.IsError() {
		return exec.ValueError(fmt.Errorf("%s", p.Error()))
	}
	a, b := toList(in), toList(p.Args[0])
	out := make([]any, 0, len(a))
	for _, v := range a {
		if listContains(b, v) {
			out = append(out, v)
		}
	}
	return exec.AsValue(dedupPreserveOrder(out))
}

func filterDifference(e *exec.Evaluator, in *exec.Value, params *exec.VarArgs) *exec.Value {
	p := params.Expect(1, nil)
	if p.IsError() {
		return exec.ValueError(fmt.Errorf("%s", p.Error()))
	}
	a, b := toList(in), toList(p.Args[0])
	out := make([]any, 0, len(a))
	for _, v := range a {
		if !listContains(b, v) {
			out = append(out, v)
		}
	}
	return exec.AsValue(dedupPreserveOrder(out))
}

func filterSymmetricDifference(e *exec.Evaluator, in *exec.Value, params *exec.VarArgs) *exec.Value {
	p := params.Expect(1, nil)
	if p.IsError() {
		return exec.ValueError(fmt.Errorf("%s", p.Error()))
	}
	a, b := toList(in), toList(p.Args[0])
	union := dedupPreserveOrder(append(append([]any{}, a...), b...))
	isect := make([]any, 0)
	for _, v := range a {
		if listContains(b, v) {
			isect = append(isect, v)
		}
	}
	out := make([]any, 0, len(union))
	for _, v := range union {
		if !listContains(isect, v) {
			out = append(out, v)
		}
	}
	return exec.AsValue(out)
}

// floatArg reads an optional numeric argument by position, falling back to
// the same name given as a keyword argument, then to fallback — matching
// real Ansible's Python calling convention where log/root's "base" is a
// normal positional-or-keyword parameter.
func floatArg(params *exec.VarArgs, index int, name string, fallback float64) float64 {
	if len(params.Args) > index {
		return params.Args[index].Float()
	}
	if kw, ok := params.KwArgs[name]; ok {
		return kw.Float()
	}
	return fallback
}

func filterLog(e *exec.Evaluator, in *exec.Value, params *exec.VarArgs) *exec.Value {
	base := floatArg(params, 0, "base", math.E)
	x := in.Float()
	if base == 10 {
		return exec.AsValue(math.Log10(x))
	}
	return exec.AsValue(math.Log(x) / math.Log(base))
}

func filterPow(e *exec.Evaluator, in *exec.Value, params *exec.VarArgs) *exec.Value {
	p := params.Expect(1, nil)
	if p.IsError() {
		return exec.ValueError(fmt.Errorf("%s", p.Error()))
	}
	return exec.AsValue(math.Pow(in.Float(), p.Args[0].Float()))
}

func filterRoot(e *exec.Evaluator, in *exec.Value, params *exec.VarArgs) *exec.Value {
	base := floatArg(params, 0, "base", 2)
	x := in.Float()
	if base == 2 {
		return exec.AsValue(math.Sqrt(x))
	}
	return exec.AsValue(math.Pow(x, 1.0/base))
}

// sizeRanges mirrors real Ansible's own SIZE_RANGES table
// (ansible.module_utils.common.text.formatters), largest unit first —
// already the order bytes_to_human needs and dict-insertion order gives its
// Python original.
var sizeRanges = []struct {
	suffix string
	limit  float64
}{
	{"Y", math.Pow(2, 80)},
	{"Z", math.Pow(2, 70)},
	{"E", math.Pow(2, 60)},
	{"P", math.Pow(2, 50)},
	{"T", math.Pow(2, 40)},
	{"G", math.Pow(2, 30)},
	{"M", math.Pow(2, 20)},
	{"K", math.Pow(2, 10)},
	{"B", 1},
}

// validUnits mirrors real Ansible's VALID_UNITS table: for each range key,
// the [byte-name, bit-name] pair accepted as a spelled-out unit.
var validUnits = map[string][2][2]string{
	"B": {{"byte", "B"}, {"bit", "b"}},
	"K": {{"kilobyte", "KB"}, {"kilobit", "Kb"}},
	"M": {{"megabyte", "MB"}, {"megabit", "Mb"}},
	"G": {{"gigabyte", "GB"}, {"gigabit", "Gb"}},
	"T": {{"terabyte", "TB"}, {"terabit", "Tb"}},
	"P": {{"petabyte", "PB"}, {"petabit", "Pb"}},
	"E": {{"exabyte", "EB"}, {"exabit", "Eb"}},
	"Z": {{"zetabyte", "ZB"}, {"zetabit", "Zb"}},
	"Y": {{"yottabyte", "YB"}, {"yottabit", "Yb"}},
}

var humanToBytesRe = regexp.MustCompile(`^([0-9]*\.?[0-9]+)(?:\s*([A-Za-z]+))?\s*$`)

// humanToBytesValue ports formatters.py's human_to_bytes() exactly,
// including its two-tier unit check: a single-letter suffix ("M") always
// matches, a spelled-out one must match VALID_UNITS' byte/bit form for that
// range and for isbits.
func humanToBytesValue(number, defaultUnit string, isbits bool) (int64, error) {
	m := humanToBytesRe.FindStringSubmatch(strings.TrimSpace(number))
	if m == nil {
		return 0, fmt.Errorf("can't interpret following string: %s", number)
	}
	num, err := strconv.ParseFloat(m[1], 64)
	if err != nil {
		return 0, fmt.Errorf("can't interpret following number: %s (original input string: %s)", m[1], number)
	}
	unit := m[2]
	if unit == "" {
		unit = defaultUnit
	}
	if unit == "" {
		return int64(math.Round(num)), nil
	}
	rangeKey := strings.ToUpper(unit[:1])
	var limit float64
	found := false
	for _, r := range sizeRanges {
		if r.suffix == rangeKey {
			limit, found = r.limit, true
			break
		}
	}
	if !found {
		keys := make([]string, len(sizeRanges))
		for i, r := range sizeRanges {
			keys[i] = r.suffix
		}
		return 0, fmt.Errorf("failed to convert %s (unit = %s). The suffix must be one of %s", number, unit, strings.Join(keys, ", "))
	}

	unitClass, unitClassName := "B", "byte"
	if isbits {
		unitClass, unitClassName = "b", "bit"
	}
	if len(unit) > 1 {
		expect := fmt.Sprintf("expect %s%s or %s", rangeKey, unitClass, rangeKey)
		if rangeKey == "B" {
			expect = fmt.Sprintf("expect %s or %s", unitClass, unitClassName)
		}
		pair := validUnits[rangeKey]
		idx := 0
		if isbits {
			idx = 1
		}
		switch {
		case strings.ToLower(unit) == pair[idx][0]:
		case unit != pair[idx][1]:
			return 0, fmt.Errorf("failed to convert %s. Value is not a valid string (%s)", number, expect)
		}
	}
	return int64(math.Round(num * limit)), nil
}

// bytesToHumanValue ports formatters.py's bytes_to_human() exactly: unit,
// when given, must be the single-letter range key (real Ansible compares
// the whole uppercased unit string against that one letter, so a spelled-
// out unit like "MB" never matches and falls through to the smallest
// range, exactly as it does in the Python original).
func bytesToHumanValue(size float64, isbits bool, unit string) string {
	base := "Bytes"
	if isbits {
		base = "bits"
	}
	suffix, limit := sizeRanges[len(sizeRanges)-1].suffix, sizeRanges[len(sizeRanges)-1].limit
	for _, r := range sizeRanges {
		suffix, limit = r.suffix, r.limit
		if (unit == "" && size >= limit) || (unit != "" && strings.ToUpper(unit) == r.suffix) {
			break
		}
	}
	if limit != 1 {
		suffix += base[:1]
	} else {
		suffix = base
	}
	return fmt.Sprintf("%.2f %s", size/limit, suffix)
}

func filterHumanReadable(e *exec.Evaluator, in *exec.Value, params *exec.VarArgs) *exec.Value {
	isbits := params.GetKeywordArgument("isbits", exec.AsValue(false)).IsTrue()
	unit := params.GetKeywordArgument("unit", exec.AsValue("")).String()
	if len(params.Args) > 0 {
		isbits = params.Args[0].IsTrue()
	}
	if len(params.Args) > 1 {
		unit = params.Args[1].String()
	}
	return exec.AsValue(bytesToHumanValue(in.Float(), isbits, unit))
}

func filterHumanToBytes(e *exec.Evaluator, in *exec.Value, params *exec.VarArgs) *exec.Value {
	defaultUnit := params.GetKeywordArgument("default_unit", exec.AsValue("")).String()
	isbits := params.GetKeywordArgument("isbits", exec.AsValue(false)).IsTrue()
	if len(params.Args) > 0 {
		defaultUnit = params.Args[0].String()
	}
	if len(params.Args) > 1 {
		isbits = params.Args[1].IsTrue()
	}
	out, err := humanToBytesValue(in.String(), defaultUnit, isbits)
	if err != nil {
		return exec.ValueError(fmt.Errorf("human_to_bytes: %w", err))
	}
	return exec.AsValue(out)
}

func filterRekeyOnMember(e *exec.Evaluator, in *exec.Value, params *exec.VarArgs) *exec.Value {
	p := params.Expect(1, []*exec.KwArg{{Name: "duplicates", Default: "error"}})
	if p.IsError() {
		return exec.ValueError(fmt.Errorf("%s", p.Error()))
	}
	key := p.Args[0].String()
	duplicates := p.KwArgs["duplicates"].String()
	if duplicates != "error" && duplicates != "overwrite" {
		return exec.ValueError(fmt.Errorf("rekey_on_member: duplicates parameter has unknown value %q", duplicates))
	}

	var items []any
	switch t := in.ToGoSimpleType(false).(type) {
	case map[string]any:
		items = make([]any, 0, len(t))
		for _, v := range t {
			items = append(items, v)
		}
	case []any:
		items = t
	default:
		return exec.ValueError(fmt.Errorf("rekey_on_member: type is not a valid list, set, or dict"))
	}

	out := map[string]any{}
	for _, item := range items {
		m, ok := item.(map[string]any)
		if !ok {
			return exec.ValueError(fmt.Errorf("rekey_on_member: list item is not a valid dict"))
		}
		keyElem, ok := m[key]
		if !ok {
			return exec.ValueError(fmt.Errorf("rekey_on_member: key %q was not found", key))
		}
		keyStr := fmt.Sprintf("%v", keyElem)
		if _, exists := out[keyStr]; exists && duplicates == "error" {
			return exec.ValueError(fmt.Errorf("rekey_on_member: key %q is not unique, cannot convert to dict", keyStr))
		}
		out[keyStr] = m
	}
	return exec.AsValue(out)
}

// ansibleUUIDNamespace is Ansible's own fixed default namespace for
// to_uuid, UUID_NAMESPACE_ANSIBLE in ansible-core's core.py filters.
var ansibleUUIDNamespace = mustParseUUID("361E6D51-FAEC-444A-9079-341386DA8E2E")

func mustParseUUID(s string) [16]byte {
	u, err := parseUUID(s)
	if err != nil {
		panic(err)
	}
	return u
}

func parseUUID(s string) ([16]byte, error) {
	var u [16]byte
	hexDigits := strings.ReplaceAll(s, "-", "")
	if len(hexDigits) != 32 {
		return u, fmt.Errorf("invalid UUID %q", s)
	}
	b, err := hex.DecodeString(hexDigits)
	if err != nil {
		return u, fmt.Errorf("invalid UUID %q: %w", s, err)
	}
	copy(u[:], b)
	return u, nil
}

// uuidV5 is RFC 4122 UUID version 5 (SHA-1 name-based), matching Python's
// uuid.uuid5(namespace, name) exactly.
func uuidV5(namespace [16]byte, name string) string {
	h := sha1.New()
	h.Write(namespace[:])
	h.Write([]byte(name))
	sum := h.Sum(nil)
	var u [16]byte
	copy(u[:], sum[:16])
	u[6] = (u[6] & 0x0F) | 0x50
	u[8] = (u[8] & 0x3F) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", u[0:4], u[4:6], u[6:8], u[8:10], u[10:16])
}

func filterToUUID(e *exec.Evaluator, in *exec.Value, params *exec.VarArgs) *exec.Value {
	namespace := ansibleUUIDNamespace
	namespaceStr := ""
	if len(params.Args) > 0 {
		namespaceStr = params.Args[0].String()
	} else if kw, ok := params.KwArgs["namespace"]; ok {
		namespaceStr = kw.String()
	}
	if namespaceStr != "" {
		ns, err := parseUUID(namespaceStr)
		if err != nil {
			return exec.ValueError(fmt.Errorf("to_uuid: %w", err))
		}
		namespace = ns
	}
	return exec.AsValue(uuidV5(namespace, in.String()))
}
