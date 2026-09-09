package template

import (
	"crypto/md5"
	"crypto/rand"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash"
	"math"
	"os"
	"os/user"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"regexp"

	"github.com/go-encryptions/unixcrypt"
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
	sha1Hex := func(b []byte) string { s := sha1.Sum(b); return hex.EncodeToString(s[:]) }
	must("md5", filterHash(func(b []byte) string { s := md5.Sum(b); return hex.EncodeToString(s[:]) }))
	must("sha1", filterHash(sha1Hex))
	// checksum is real Ansible's own alias for sha1 (ansible.utils.hashing's
	// checksum_s defaults to sha1) — the same digest, a different name.
	must("checksum", filterHash(sha1Hex))
	must("hash", filterHashGeneric)
	must("password_hash", filterPasswordHash)

	must("path_join", filterPathJoin)
	must("splitext", filterSplitext)
	must("expanduser", filterExpandUser)
	must("expandvars", filterExpandVars)
	must("realpath", filterRealpath)
	must("relpath", filterRelpath)
	must("normpath", filterNormpath)
	must("commonpath", filterCommonpath)
	must("win_basename", filterWinBasename)
	must("win_dirname", filterWinDirname)
	must("win_splitdrive", filterWinSplitdrive)

	must("comment", filterComment)

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

	// mathstuff.py's own combinatorial section registers Python's
	// itertools functions (and the zip builtin) directly, with no
	// Ansible-specific wrapper — real filter calls carry their exact
	// Python calling convention straight through.
	must("product", filterProduct)
	must("permutations", filterPermutations)
	must("combinations", filterCombinations)
	must("zip", filterZip)
	must("zip_longest", filterZipLongest)

	must("extract", filterExtract)
	must("flatten", filterFlatten)
	must("subelements", filterSubelements)
	must("split", filterSplit)

	must("fileglob", filterFileglob)

	must("to_datetime", filterToDatetime)
	must("strftime", filterStrftime)
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

// posixSplit ports posixpath.split(p) exactly: tail is everything after the
// final "/" (empty if p ends in "/"), head is everything before it with any
// trailing "/"es stripped (unless head is all slashes, i.e. the root).
// Deliberately NOT Go's path.Base/path.Dir, which Clean the result first —
// a real, previously-shipped divergence from Python found while porting the
// rest of this file's path filters: path.Base("/foo/bar/") is "bar" where
// Python's basename is "" (empty), and path.Dir("foo")/path.Base("") are
// "." where Python's dirname/basename are "" (empty).
func posixSplit(p string) (head, tail string) {
	i := strings.LastIndexByte(p, '/') + 1
	head, tail = p[:i], p[i:]
	if head != "" && strings.Trim(head, "/") != "" {
		head = strings.TrimRight(head, "/")
	}
	return head, tail
}

func filterBasename(e *exec.Evaluator, in *exec.Value, params *exec.VarArgs) *exec.Value {
	_, tail := posixSplit(in.String())
	return exec.AsValue(tail)
}

func filterDirname(e *exec.Evaluator, in *exec.Value, params *exec.VarArgs) *exec.Value {
	head, _ := posixSplit(in.String())
	return exec.AsValue(head)
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

// hashConstructors covers the hashlib names real Ansible's own generic
// "hash" filter is documented and commonly used with.
var hashConstructors = map[string]func() hash.Hash{
	"md5":    md5.New,
	"sha1":   sha1.New,
	"sha224": sha256.New224,
	"sha256": sha256.New,
	"sha384": sha512.New384,
	"sha512": sha512.New,
}

// filterHashGeneric ports real Ansible's own "hash" filter: get_hash(data,
// hashtype='sha1') — defaults to sha1 (NOT sha256, a real bug in this port
// found and fixed while reading core.py's own filters() registration:
// 'hash': get_hash defaults hashtype='sha1', confirmed from source), and
// accepts an algorithm name as an optional argument.
func filterHashGeneric(e *exec.Evaluator, in *exec.Value, params *exec.VarArgs) *exec.Value {
	hashtype := "sha1"
	if len(params.Args) > 0 {
		hashtype = params.Args[0].String()
	} else if kw, ok := params.KwArgs["hashtype"]; ok {
		hashtype = kw.String()
	}
	newHash, ok := hashConstructors[strings.ToLower(hashtype)]
	if !ok {
		names := make([]string, 0, len(hashConstructors))
		for name := range hashConstructors {
			names = append(names, name)
		}
		sort.Strings(names)
		return exec.ValueError(fmt.Errorf("hash: unsupported hash type %q, must be one of %s", hashtype, strings.Join(names, ", ")))
	}
	h := newHash()
	h.Write([]byte(in.String()))
	return exec.AsValue(hex.EncodeToString(h.Sum(nil)))
}

// --- Path filters ---
//
// Ported from real ansible-core's own core.py path section, which is
// itself a thin wrap over Python's os.path (posixpath on the Linux/macOS
// controllers this port targets) and ntpath (for the win_* filters). Real
// Python's stdlib source (posixpath.py/ntpath.py/genericpath.py) was read
// directly as the bibliography here, since these filters ARE that stdlib
// behavior, not a separate Ansible algorithm. Implemented independent of
// Go's os/path/filepath packages' own OS-dependent Clean-ing (see
// posixSplit's own comment) so behavior stays POSIX regardless of GOOS.

// posixSplitRoot ports posixpath's splitroot: root is "" for a relative
// path, "/" for one or 3+ leading slashes, or "//" for EXACTLY two — a real
// POSIX-defined special case (see IEEE Std 1003.1) normpath must preserve.
func posixSplitRoot(p string) (root, tail string) {
	if !strings.HasPrefix(p, "/") {
		return "", p
	}
	if !strings.HasPrefix(p, "//") || strings.HasPrefix(p, "///") {
		return "/", p[1:]
	}
	return "//", p[2:]
}

func posixNormpath(p string) string {
	if p == "" {
		return "."
	}
	root, tail := posixSplitRoot(p)
	comps := strings.Split(tail, "/")
	newComps := make([]string, 0, len(comps))
	for _, comp := range comps {
		switch {
		case comp == "" || comp == ".":
			continue
		case comp != ".." || (root == "" && len(newComps) == 0) || (len(newComps) > 0 && newComps[len(newComps)-1] == ".."):
			newComps = append(newComps, comp)
		case len(newComps) > 0:
			newComps = newComps[:len(newComps)-1]
		}
	}
	result := root + strings.Join(newComps, "/")
	if result == "" {
		return "."
	}
	return result
}

func posixJoin(paths []string) string {
	if len(paths) == 0 {
		return ""
	}
	out := paths[0]
	for _, b := range paths[1:] {
		switch {
		case strings.HasPrefix(b, "/") || out == "":
			out = b
		case strings.HasSuffix(out, "/"):
			out += b
		default:
			out += "/" + b
		}
	}
	return out
}

func posixAbspath(p string) string {
	if !strings.HasPrefix(p, "/") {
		if wd, err := os.Getwd(); err == nil {
			p = posixJoin([]string{wd, p})
		}
	}
	return posixNormpath(p)
}

func filterPathJoin(e *exec.Evaluator, in *exec.Value, params *exec.VarArgs) *exec.Value {
	if in.IsString() {
		// Real path_join(paths) calls os.path.join(paths) with a single
		// arg when paths is a string — an identity no-op, no join logic
		// ever runs.
		return exec.AsValue(in.String())
	}
	list := toList(in)
	if list == nil {
		return exec.ValueError(fmt.Errorf("path_join expects a string or a list, got %T", in.Interface()))
	}
	parts := make([]string, len(list))
	for i, v := range list {
		parts[i] = fmt.Sprintf("%v", v)
	}
	return exec.AsValue(posixJoin(parts))
}

func filterNormpath(e *exec.Evaluator, in *exec.Value, params *exec.VarArgs) *exec.Value {
	return exec.AsValue(posixNormpath(in.String()))
}

func filterRealpath(e *exec.Evaluator, in *exec.Value, params *exec.VarArgs) *exec.Value {
	abs := posixAbspath(in.String())
	// Real os.path.realpath resolves symlinks component-by-component,
	// tolerating a nonexistent trailing component. filepath.EvalSymlinks
	// instead errors on any nonexistent component; falling back to the
	// plain absolute+normalized path there is a disclosed simplification
	// for that one edge case, not a literal reproduction of Python's own
	// partial-resolution algorithm.
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		return exec.AsValue(resolved)
	}
	return exec.AsValue(abs)
}

func filterRelpath(e *exec.Evaluator, in *exec.Value, params *exec.VarArgs) *exec.Value {
	start := "."
	if len(params.Args) > 0 {
		start = params.Args[0].String()
	}
	startTail := strings.TrimLeft(posixAbspath(start), "/")
	pathTail := strings.TrimLeft(posixAbspath(in.String()), "/")
	var startList, pathList []string
	if startTail != "" {
		startList = strings.Split(startTail, "/")
	}
	if pathTail != "" {
		pathList = strings.Split(pathTail, "/")
	}
	i := 0
	for i < len(startList) && i < len(pathList) && startList[i] == pathList[i] {
		i++
	}
	relList := make([]string, 0, (len(startList)-i)+(len(pathList)-i))
	for k := 0; k < len(startList)-i; k++ {
		relList = append(relList, "..")
	}
	relList = append(relList, pathList[i:]...)
	if len(relList) == 0 {
		return exec.AsValue(".")
	}
	return exec.AsValue(strings.Join(relList, "/"))
}

// genericSplitext ports genericpath._splitext: the extension is everything
// from the last dot to the end, ignoring leading dots (so ".bashrc" and
// "..bashrc" have NO extension, but "a." does — an empty name before a
// trailing dot still counts).
func genericSplitext(p, seps string) (root, ext string) {
	sepIndex := strings.LastIndexAny(p, seps)
	dotIndex := strings.LastIndexByte(p, '.')
	if dotIndex > sepIndex {
		filenameIndex := sepIndex + 1
		for filenameIndex < dotIndex {
			if p[filenameIndex] != '.' {
				return p[:dotIndex], p[dotIndex:]
			}
			filenameIndex++
		}
	}
	return p, ""
}

func filterSplitext(e *exec.Evaluator, in *exec.Value, params *exec.VarArgs) *exec.Value {
	root, ext := genericSplitext(in.String(), "/")
	return exec.AsValue([]any{root, ext})
}

func filterCommonpath(e *exec.Evaluator, in *exec.Value, params *exec.VarArgs) *exec.Value {
	list := toList(in)
	if len(list) == 0 {
		return exec.ValueError(fmt.Errorf("commonpath() arg is an empty sequence"))
	}
	paths := make([]string, len(list))
	for i, v := range list {
		paths[i] = fmt.Sprintf("%v", v)
	}
	isAbs := strings.HasPrefix(paths[0], "/")
	for _, p := range paths {
		if strings.HasPrefix(p, "/") != isAbs {
			return exec.ValueError(fmt.Errorf("commonpath: can't mix absolute and relative paths"))
		}
	}
	splitPaths := make([][]string, len(paths))
	for i, p := range paths {
		filtered := make([]string, 0, len(paths))
		for _, c := range strings.Split(p, "/") {
			if c != "" && c != "." {
				filtered = append(filtered, c)
			}
		}
		splitPaths[i] = filtered
	}
	minIdx, maxIdx := 0, 0
	for i := 1; i < len(splitPaths); i++ {
		if lessStringSlice(splitPaths[i], splitPaths[minIdx]) {
			minIdx = i
		}
		if lessStringSlice(splitPaths[maxIdx], splitPaths[i]) {
			maxIdx = i
		}
	}
	s1, s2 := splitPaths[minIdx], splitPaths[maxIdx]
	common := s1
	for i, c := range s1 {
		if i >= len(s2) || c != s2[i] {
			common = s1[:i]
			break
		}
	}
	prefix := ""
	if isAbs {
		prefix = "/"
	}
	return exec.AsValue(prefix + strings.Join(common, "/"))
}

func lessStringSlice(a, b []string) bool {
	for i := 0; i < len(a) && i < len(b); i++ {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return len(a) < len(b)
}

// expandUserHome finds the current user's home directory the same way real
// Python's expanduser does for bare "~": prefer $HOME, fall back to the
// password database (os/user.Current, which itself consults getpwuid on
// POSIX) — unlike Go's own os.UserHomeDir, which only ever checks $HOME.
func expandUserHome() (string, error) {
	if h := os.Getenv("HOME"); h != "" {
		return h, nil
	}
	if u, err := user.Current(); err == nil && u.HomeDir != "" {
		return u.HomeDir, nil
	}
	return "", fmt.Errorf("no home directory")
}

func filterExpandUser(e *exec.Evaluator, in *exec.Value, params *exec.VarArgs) *exec.Value {
	p := in.String()
	if !strings.HasPrefix(p, "~") {
		return exec.AsValue(p)
	}
	i := strings.IndexByte(p, '/')
	if i < 0 {
		i = len(p)
	}
	var home string
	if i == 1 {
		h, err := expandUserHome()
		if err != nil {
			return exec.AsValue(p)
		}
		home = h
	} else {
		u, err := user.Lookup(p[1:i])
		if err != nil {
			return exec.AsValue(p)
		}
		home = u.HomeDir
	}
	home = strings.TrimRight(home, "/")
	rest := p[i:]
	if home == "" && rest == "" {
		return exec.AsValue("/")
	}
	return exec.AsValue(home + rest)
}

var expandVarsRe = regexp.MustCompile(`\$(\w+|\{[^}]*\}?)`)

func filterExpandVars(e *exec.Evaluator, in *exec.Value, params *exec.VarArgs) *exec.Value {
	p := in.String()
	if !strings.Contains(p, "$") {
		return exec.AsValue(p)
	}
	return exec.AsValue(expandVarsRe.ReplaceAllStringFunc(p, func(m string) string {
		name := m[1:]
		if strings.HasPrefix(name, "{") {
			if !strings.HasSuffix(name, "}") {
				return m
			}
			name = name[1 : len(name)-1]
		}
		if v, ok := os.LookupEnv(name); ok {
			return v
		}
		return m
	}))
}

// ntSplitRoot ports ntpath's splitroot for the common cases: a drive
// letter ("C:\...", "C:..."), a UNC/device share ("\\server\share\...",
// "\\.\device\..."), a rooted-relative path ("\Windows"), and a plain
// relative path. The rare "\\?\UNC\server\share" extended-length-prefix
// form is a disclosed simplification NOT specially detected (it falls
// through as an ordinary UNC-shaped path instead) — narrow enough in
// practice not to warrant the extra 8-char prefix check real ntpath does.
func ntSplitRoot(p string) (drive, root, tail string) {
	normp := strings.ReplaceAll(p, "/", `\`)
	switch {
	case strings.HasPrefix(normp, `\`):
		if !strings.HasPrefix(normp[1:], `\`) {
			return "", p[:1], p[1:]
		}
		idx := strings.Index(normp[2:], `\`)
		if idx == -1 {
			return p, "", ""
		}
		idx += 2
		idx2 := strings.Index(normp[idx+1:], `\`)
		if idx2 == -1 {
			return p, "", ""
		}
		idx2 += idx + 1
		return p[:idx2], p[idx2 : idx2+1], p[idx2+1:]
	case len(normp) > 1 && normp[1] == ':':
		if len(normp) > 2 && normp[2] == '\\' {
			return p[:2], p[2:3], p[3:]
		}
		return p[:2], "", p[2:]
	default:
		return "", "", p
	}
}

func ntSplit(p string) (head, tail string) {
	drive, root, rest := ntSplitRoot(p)
	i := len(rest)
	for i > 0 && rest[i-1] != '\\' && rest[i-1] != '/' {
		i--
	}
	head, tail = rest[:i], rest[i:]
	return drive + root + strings.TrimRight(head, `\/`), tail
}

func filterWinBasename(e *exec.Evaluator, in *exec.Value, params *exec.VarArgs) *exec.Value {
	_, tail := ntSplit(in.String())
	return exec.AsValue(tail)
}

func filterWinDirname(e *exec.Evaluator, in *exec.Value, params *exec.VarArgs) *exec.Value {
	head, _ := ntSplit(in.String())
	return exec.AsValue(head)
}

func filterWinSplitdrive(e *exec.Evaluator, in *exec.Value, params *exec.VarArgs) *exec.Value {
	drive, root, tail := ntSplitRoot(in.String())
	return exec.AsValue([]any{drive, root + tail})
}

// --- comment filter ---

type commentStyle struct{ beginning, decoration, end string }

// commentStyleNames preserves real Ansible's own dict-insertion order
// (plain, erlang, c, cblock, xml) for the "invalid style" error message,
// since map iteration order in Go is random.
var commentStyleNames = []string{"plain", "erlang", "c", "cblock", "xml"}

var commentStyles = map[string]commentStyle{
	"plain":  {decoration: "# "},
	"erlang": {decoration: "% "},
	"c":      {decoration: "// "},
	"cblock": {beginning: "/*", decoration: " * ", end: " */"},
	"xml":    {beginning: "<!--", decoration: " - ", end: "-->"},
}

// filterComment ports real ansible-core's own comment() filter, a fiddly
// but fully deterministic string-composition function — traced by hand
// against real Ansible's documented plain/cblock examples before writing
// the Go version, not just transliterated line-by-line.
func filterComment(e *exec.Evaluator, in *exec.Value, params *exec.VarArgs) *exec.Value {
	style := "plain"
	if len(params.Args) > 0 {
		style = params.Args[0].String()
	} else if kw, ok := params.KwArgs["style"]; ok {
		style = kw.String()
	}
	styleParams, ok := commentStyles[style]
	if !ok {
		return exec.ValueError(fmt.Errorf("comment: invalid style %q. Available styles: %s", style, strings.Join(commentStyleNames, ", ")))
	}

	prepostfix := styleParams.decoration
	if kw, ok := params.KwArgs["decoration"]; ok {
		prepostfix = kw.String()
	}

	newline := "\n"
	if kw, ok := params.KwArgs["newline"]; ok {
		newline = kw.String()
	}
	beginning := styleParams.beginning
	if kw, ok := params.KwArgs["beginning"]; ok {
		beginning = kw.String()
	}
	prefix := strings.TrimRightFunc(prepostfix, unicode.IsSpace)
	if kw, ok := params.KwArgs["prefix"]; ok {
		prefix = kw.String()
	}
	prefixCount := 1
	if kw, ok := params.KwArgs["prefix_count"]; ok {
		prefixCount = kw.Integer()
	}
	decoration := styleParams.decoration
	if kw, ok := params.KwArgs["decoration"]; ok {
		decoration = kw.String()
	}
	postfix := strings.TrimRightFunc(prepostfix, unicode.IsSpace)
	if kw, ok := params.KwArgs["postfix"]; ok {
		postfix = kw.String()
	}
	postfixCount := 1
	if kw, ok := params.KwArgs["postfix_count"]; ok {
		postfixCount = kw.Integer()
	}
	end := styleParams.end
	if kw, ok := params.KwArgs["end"]; ok {
		end = kw.String()
	}

	text := in.String()

	var strBeginning string
	if beginning != "" {
		strBeginning = beginning + newline
	}

	var strPrefix string
	if prefix != "" {
		if prefix != newline {
			strPrefix = strings.Repeat(prefix+newline, prefixCount)
		} else {
			strPrefix = strings.Repeat(newline, prefixCount)
		}
	}

	strText := decoration + strings.ReplaceAll(text, newline, newline+decoration)
	strText = strings.ReplaceAll(strText, decoration+newline, strings.TrimRightFunc(decoration, unicode.IsSpace)+newline)

	postfixParts := make([]string, 0, postfixCount+1)
	postfixParts = append(postfixParts, "")
	for i := 0; i < postfixCount; i++ {
		postfixParts = append(postfixParts, postfix)
	}
	strPostfix := strings.Join(postfixParts, newline)

	var strEnd string
	if end != "" {
		strEnd = newline + end
	}

	return exec.AsValue(strBeginning + strPrefix + strText + strPostfix + strEnd)
}

// --- Combinatorial filters ---
//
// mathstuff.py registers Python's itertools.product/permutations/
// combinations and the zip/itertools.zip_longest builtins directly as
// filters, with no Ansible-specific wrapper function at all — so what
// these filters need to match is Python's own algorithms, including their
// exact OUTPUT ORDER (a caller may depend on it), not just their result
// set. Each generator below is a direct, hand-traced port of the iterative
// algorithm documented for that itertools function, verified by tracing
// permutations([1,2,3], 2) and comparing against real Python's own
// documented output before trusting it.

func cartesianProduct(iterables [][]any) [][]any {
	result := [][]any{{}}
	for _, it := range iterables {
		next := make([][]any, 0, len(result)*len(it))
		for _, r := range result {
			for _, v := range it {
				tuple := make([]any, len(r)+1)
				copy(tuple, r)
				tuple[len(r)] = v
				next = append(next, tuple)
			}
		}
		result = next
	}
	return result
}

func filterProduct(e *exec.Evaluator, in *exec.Value, params *exec.VarArgs) *exec.Value {
	base := [][]any{toList(in)}
	for _, arg := range params.Args {
		base = append(base, toList(arg))
	}
	repeat := 1
	if kw, ok := params.KwArgs["repeat"]; ok {
		repeat = kw.Integer()
	}
	iterables := make([][]any, 0, len(base)*repeat)
	for i := 0; i < repeat; i++ {
		iterables = append(iterables, base...)
	}
	tuples := cartesianProduct(iterables)
	out := make([]any, len(tuples))
	for i, t := range tuples {
		out[i] = t
	}
	return exec.AsValue(out)
}

// permutationsOf ports itertools.permutations' own documented iterative
// algorithm (index-cycling, not a naive recursive generator) to guarantee
// the exact same output order real Python produces.
func permutationsOf(pool []any, r int) [][]any {
	n := len(pool)
	if r < 0 || r > n {
		return nil
	}
	if r == 0 {
		return [][]any{{}}
	}
	indices := make([]int, n)
	for i := range indices {
		indices[i] = i
	}
	cycles := make([]int, r)
	for i := 0; i < r; i++ {
		cycles[i] = n - i
	}
	emit := func() []any {
		tuple := make([]any, r)
		for i := 0; i < r; i++ {
			tuple[i] = pool[indices[i]]
		}
		return tuple
	}
	result := [][]any{emit()}
	for {
		advanced := false
		for i := r - 1; i >= 0; i-- {
			cycles[i]--
			if cycles[i] == 0 {
				rest := append(append([]int{}, indices[i+1:]...), indices[i])
				copy(indices[i:], rest)
				cycles[i] = n - i
			} else {
				j := cycles[i]
				indices[i], indices[n-j] = indices[n-j], indices[i]
				result = append(result, emit())
				advanced = true
				break
			}
		}
		if !advanced {
			break
		}
	}
	return result
}

func filterPermutations(e *exec.Evaluator, in *exec.Value, params *exec.VarArgs) *exec.Value {
	list := toList(in)
	r := len(list)
	if len(params.Args) > 0 {
		r = params.Args[0].Integer()
	} else if kw, ok := params.KwArgs["r"]; ok {
		r = kw.Integer()
	}
	tuples := permutationsOf(list, r)
	out := make([]any, len(tuples))
	for i, t := range tuples {
		out[i] = t
	}
	return exec.AsValue(out)
}

// combinationsOf ports itertools.combinations' own documented iterative
// algorithm, same rationale as permutationsOf.
func combinationsOf(pool []any, r int) [][]any {
	n := len(pool)
	if r < 0 || r > n {
		return nil
	}
	indices := make([]int, r)
	for i := range indices {
		indices[i] = i
	}
	emit := func() []any {
		tuple := make([]any, r)
		for i := 0; i < r; i++ {
			tuple[i] = pool[indices[i]]
		}
		return tuple
	}
	result := [][]any{emit()}
	for {
		found := -1
		for i := r - 1; i >= 0; i-- {
			if indices[i] != i+n-r {
				found = i
				break
			}
		}
		if found == -1 {
			break
		}
		indices[found]++
		for j := found + 1; j < r; j++ {
			indices[j] = indices[j-1] + 1
		}
		result = append(result, emit())
	}
	return result
}

func filterCombinations(e *exec.Evaluator, in *exec.Value, params *exec.VarArgs) *exec.Value {
	p := params.Expect(1, nil)
	if p.IsError() {
		return exec.ValueError(fmt.Errorf("%s", p.Error()))
	}
	tuples := combinationsOf(toList(in), p.Args[0].Integer())
	out := make([]any, len(tuples))
	for i, t := range tuples {
		out[i] = t
	}
	return exec.AsValue(out)
}

func filterZip(e *exec.Evaluator, in *exec.Value, params *exec.VarArgs) *exec.Value {
	lists := [][]any{toList(in)}
	for _, arg := range params.Args {
		lists = append(lists, toList(arg))
	}
	minLen := 0
	for i, l := range lists {
		if i == 0 || len(l) < minLen {
			minLen = len(l)
		}
	}
	out := make([]any, minLen)
	for i := 0; i < minLen; i++ {
		tuple := make([]any, len(lists))
		for j, l := range lists {
			tuple[j] = l[i]
		}
		out[i] = tuple
	}
	return exec.AsValue(out)
}

func filterZipLongest(e *exec.Evaluator, in *exec.Value, params *exec.VarArgs) *exec.Value {
	lists := [][]any{toList(in)}
	for _, arg := range params.Args {
		lists = append(lists, toList(arg))
	}
	var fillvalue any
	if kw, ok := params.KwArgs["fillvalue"]; ok {
		fillvalue = kw.Interface()
	}
	maxLen := 0
	for _, l := range lists {
		if len(l) > maxLen {
			maxLen = len(l)
		}
	}
	out := make([]any, maxLen)
	for i := 0; i < maxLen; i++ {
		tuple := make([]any, len(lists))
		for j, l := range lists {
			if i < len(l) {
				tuple[j] = l[i]
			} else {
				tuple[j] = fillvalue
			}
		}
		out[i] = tuple
	}
	return exec.AsValue(out)
}

// filterExtract ports core.py's extract(): {{ key | extract(container) }}
// looks up container[key], and {{ key | extract(container, morekeys) }}
// chains further lookups (morekeys a single key, or a list of them) —
// container[key][morekeys[0]][morekeys[1]].... Real Ansible's own
// implementation falls back to environment.getitem's lazy-marker source on
// a failed lookup; this port has no such marker system, so a failed
// lookup at any step returns nil, matching this file's own established
// silent-coerce-on-miss convention (see filterDict2Items/filterItems2Dict).
func filterExtract(e *exec.Evaluator, in *exec.Value, params *exec.VarArgs) *exec.Value {
	p := params.Expect(1, []*exec.KwArg{{Name: "morekeys", Default: nil}})
	if p.IsError() {
		return exec.ValueError(fmt.Errorf("%s", p.Error()))
	}
	keys := []*exec.Value{in}
	if mk := p.KwArgs["morekeys"]; !mk.IsNil() {
		if mk.IsList() {
			// Iterate's (key, value) pair carries a list's own element in
			// key, not value — value is nil for a plain list (confirmed
			// against gonja's own filterSelectAttr, which reads the same
			// way).
			mk.Iterate(func(_, _ int, key, _ *exec.Value) bool {
				keys = append(keys, key)
				return true
			}, func() {})
		} else {
			keys = append(keys, mk)
		}
	}

	value := p.Args[0]
	for _, key := range keys {
		var k any
		switch {
		case key.IsString():
			k = key.String()
		case key.IsInteger():
			k = key.Integer()
		default:
			k = key.Interface()
		}
		item, found := value.GetItem(k)
		if !found && key.IsString() {
			item, found = value.GetAttribute(key.String())
		}
		if !found {
			return exec.AsValue(nil)
		}
		value = item
	}
	return value
}

// flattenList ports core.py's flatten(): real Ansible treats Python None
// AND the literal strings "None"/"null" as null-like when skip_nulls is
// set — a real, slightly unusual detail, reproduced rather than narrowed
// to just nil.
func flattenList(list []any, levels *int, skipNulls bool) []any {
	ret := make([]any, 0, len(list))
	for _, element := range list {
		if skipNulls && (element == nil || element == "None" || element == "null") {
			continue
		}
		sub, isList := element.([]any)
		switch {
		case !isList:
			ret = append(ret, element)
		case levels == nil:
			ret = append(ret, flattenList(sub, nil, skipNulls)...)
		case *levels >= 1:
			next := *levels - 1
			ret = append(ret, flattenList(sub, &next, skipNulls)...)
		default:
			ret = append(ret, element)
		}
	}
	return ret
}

func filterFlatten(e *exec.Evaluator, in *exec.Value, params *exec.VarArgs) *exec.Value {
	var levels *int
	if len(params.Args) > 0 {
		l := params.Args[0].Integer()
		levels = &l
	} else if kw, ok := params.KwArgs["levels"]; ok && !kw.IsNil() {
		l := kw.Integer()
		levels = &l
	}
	skipNulls := true
	if kw, ok := params.KwArgs["skip_nulls"]; ok {
		skipNulls = kw.IsTrue()
	}
	return exec.AsValue(flattenList(toList(in), levels, skipNulls))
}

// filterSubelements ports core.py's subelements(): pairs each element of
// obj (a dict's values, or a list) with every item found by walking the
// dotted/list subelement accessor into that element, producing a
// cartesian-style list of [element, subvalue] pairs — real Ansible
// returns 2-tuples, represented here as 2-element []any, matching this
// file's own convention elsewhere (e.g. filterSplitext).
func filterSubelements(e *exec.Evaluator, in *exec.Value, params *exec.VarArgs) *exec.Value {
	p := params.Expect(1, []*exec.KwArg{{Name: "skip_missing", Default: false}})
	if p.IsError() {
		return exec.ValueError(fmt.Errorf("%s", p.Error()))
	}
	skipMissing := p.KwArgs["skip_missing"].IsTrue()

	var elementList []any
	switch t := in.ToGoSimpleType(false).(type) {
	case map[string]any:
		for _, v := range t {
			elementList = append(elementList, v)
		}
	case []any:
		elementList = t
	default:
		return exec.ValueError(fmt.Errorf("subelements: obj must be a list of dicts or a nested dict"))
	}

	subArg := p.Args[0]
	var subelementList []string
	switch {
	case subArg.IsList():
		for _, v := range toList(subArg) {
			subelementList = append(subelementList, fmt.Sprintf("%v", v))
		}
	case subArg.IsString():
		subelementList = strings.Split(subArg.String(), ".")
	default:
		return exec.ValueError(fmt.Errorf("subelements: must be a list or a string, got %T", subArg.Interface()))
	}

	results := make([]any, 0)
	for _, element := range elementList {
		var values any = element
		for _, sub := range subelementList {
			m, ok := values.(map[string]any)
			if !ok {
				return exec.ValueError(fmt.Errorf("subelements: the key %q should point to a dictionary, got %#v", sub, values))
			}
			v, ok := m[sub]
			if !ok {
				if skipMissing {
					values = []any{}
					break
				}
				return exec.ValueError(fmt.Errorf("subelements: could not find %q key in iterated item %#v", sub, values))
			}
			values = v
		}
		list, ok := values.([]any)
		if !ok {
			if len(subelementList) > 0 {
				return exec.ValueError(fmt.Errorf("subelements: the key should point to a list, got %#v", values))
			}
			return exec.ValueError(fmt.Errorf("subelements: subelements in the object must be a list, got %T", values))
		}
		for _, v := range list {
			results = append(results, []any{element, v})
		}
	}
	return exec.AsValue(results)
}

var splitWhitespaceRe = regexp.MustCompile(`\s+`)

// pySplitWhitespace ports Python's str.split() (no separator argument):
// splits on runs of whitespace, discarding leading/trailing empties —
// exactly strings.Fields, except when maxsplit caps the number of splits,
// where the untouched remainder (with its own original inter-word
// whitespace) becomes the final element.
func pySplitWhitespace(s string, maxsplit int) []string {
	if maxsplit < 0 {
		return strings.Fields(s)
	}
	trimmed := strings.TrimFunc(s, unicode.IsSpace)
	if trimmed == "" {
		return []string{}
	}
	locs := splitWhitespaceRe.FindAllStringIndex(trimmed, maxsplit)
	parts := make([]string, 0, len(locs)+1)
	prev := 0
	for _, loc := range locs {
		parts = append(parts, trimmed[prev:loc[0]])
		prev = loc[1]
	}
	parts = append(parts, trimmed[prev:])
	return parts
}

func filterSplit(e *exec.Evaluator, in *exec.Value, params *exec.VarArgs) *exec.Value {
	s := in.String()
	sep, hasSep := "", false
	if len(params.Args) > 0 {
		sep, hasSep = params.Args[0].String(), true
	} else if kw, ok := params.KwArgs["sep"]; ok && !kw.IsNil() {
		sep, hasSep = kw.String(), true
	}
	maxsplit := -1
	if len(params.Args) > 1 {
		maxsplit = params.Args[1].Integer()
	} else if kw, ok := params.KwArgs["maxsplit"]; ok && !kw.IsNil() {
		maxsplit = kw.Integer()
	}

	var parts []string
	if !hasSep {
		parts = pySplitWhitespace(s, maxsplit)
	} else {
		n := maxsplit
		if maxsplit >= 0 {
			n = maxsplit + 1
		}
		parts = strings.SplitN(s, sep, n)
	}
	out := make([]any, len(parts))
	for i, p := range parts {
		out[i] = p
	}
	return exec.AsValue(out)
}

// filterFileglob ports core.py's fileglob(): real Ansible is literally
// [g for g in glob.glob(pathname) if os.path.isfile(g)] — filepath.Glob
// matches Python's non-recursive glob.glob syntax closely enough (*, ?,
// [...], no ** recursion in either since Python's glob.glob doesn't
// recurse without an explicit recursive=True this filter never passes).
// One disclosed, low-stakes difference: filepath.Glob sorts its matches,
// while Python's glob.glob returns raw, filesystem-order (unsorted)
// results — a real playbook depending on that unsorted order would
// already be fragile across systems, so this port's sorted order is a
// reasonable divergence, not a bug.
func filterFileglob(e *exec.Evaluator, in *exec.Value, params *exec.VarArgs) *exec.Value {
	matches, err := filepath.Glob(in.String())
	if err != nil {
		return exec.ValueError(fmt.Errorf("fileglob: %w", err))
	}
	out := make([]any, 0, len(matches))
	for _, m := range matches {
		if fi, err := os.Stat(m); err == nil && fi.Mode().IsRegular() {
			out = append(out, m)
		}
	}
	return exec.AsValue(out)
}

// strftimeDirectives maps the common Python strptime/strftime %-directives
// (core.py's to_datetime/strftime share the same directive vocabulary,
// one parsing and one formatting) to Go's reference-time layout tokens.
// %j (day of year) has no Go layout equivalent at all and is deliberately
// absent, so it falls into the "unsupported directive" error path below
// rather than being silently mishandled — the same fail-loud choice this
// whole project makes elsewhere for a real, disclosed gap.
var strftimeDirectives = map[byte]string{
	'Y': "2006",
	'y': "06",
	'm': "01",
	'd': "02",
	'H': "15",
	'I': "03",
	'M': "04",
	'S': "05",
	'p': "PM",
	'B': "January",
	'b': "Jan",
	'A': "Monday",
	'a': "Mon",
	'z': "-0700",
	'Z': "MST",
}

// strftimeToGoLayout translates a Python %-directive format string into a
// Go reference-time layout. %f (microseconds) has no standalone Go layout
// token — Go only recognizes fractional seconds written directly adjacent
// to the seconds field — so only the common "%S.%f"/"%S,%f" idiom is
// specially handled; a bare %f elsewhere is an unsupported directive.
func strftimeToGoLayout(format string) (string, error) {
	var b strings.Builder
	for i := 0; i < len(format); i++ {
		if format[i] != '%' || i+1 >= len(format) {
			b.WriteByte(format[i])
			continue
		}
		directive := format[i+1]
		i++
		if directive == '%' {
			b.WriteByte('%')
			continue
		}
		if directive == 'S' && i+3 < len(format) && (format[i+1] == '.' || format[i+1] == ',') && format[i+2] == '%' && format[i+3] == 'f' {
			b.WriteString("05")
			b.WriteByte(format[i+1])
			b.WriteString("000000")
			i += 3
			continue
		}
		layout, ok := strftimeDirectives[directive]
		if !ok {
			return "", fmt.Errorf("unsupported strftime directive %%%c", directive)
		}
		b.WriteString(layout)
	}
	return b.String(), nil
}

// filterToDatetime ports core.py's to_datetime(): datetime.strptime(string,
// format). Real Ansible's result is a Python datetime object, meaningful
// there for both subtraction (giving a timedelta) and direct printing.
// gonja's own binary-operator evaluator only implements arithmetic for
// numeric Values (confirmed by reading exec/evaluator.go's own Subtraction
// case before choosing a representation here) — there is no hook this
// port can add without touching gonja itself, out of scope for a filter.
// So this returns the parsed time as a float64 Unix timestamp instead of
// a time.Time: it stringifies differently than Python's own datetime repr
// (a real, disclosed divergence), but (a|to_datetime) - (b|to_datetime)
// and comparisons between two results now work correctly through gonja's
// existing generic numeric operators — which is real Ansible's own
// dominant use case for this filter (elapsed-time math between two parsed
// dates), not direct printing. A format with no timezone directive parses
// as UTC (Go's own default for a tz-less layout), matching how two naive
// Python datetimes subtract to the same wall-clock delta regardless of
// what epoch either implementation privately anchors it to.
func filterToDatetime(e *exec.Evaluator, in *exec.Value, params *exec.VarArgs) *exec.Value {
	format := "%Y-%m-%d %H:%M:%S"
	if len(params.Args) > 0 {
		format = params.Args[0].String()
	} else if kw, ok := params.KwArgs["format"]; ok && !kw.IsNil() {
		format = kw.String()
	}
	layout, err := strftimeToGoLayout(format)
	if err != nil {
		return exec.ValueError(fmt.Errorf("to_datetime: %w", err))
	}
	t, err := time.Parse(layout, in.String())
	if err != nil {
		return exec.ValueError(fmt.Errorf("to_datetime: %w", err))
	}
	return exec.AsValue(float64(t.UnixNano()) / 1e9)
}

// filterStrftime ports core.py's strftime(string_format, second=None,
// utc=False): unusually for a filter, the piped-in value is the FORMAT
// string, not the time — second/utc are the filter's own arguments,
// matching real Ansible's own calling convention exactly
// ({{ '%Y-%m-%d' | strftime(some_epoch) }}).
func filterStrftime(e *exec.Evaluator, in *exec.Value, params *exec.VarArgs) *exec.Value {
	format := in.String()

	var second float64
	hasSecond := false
	if len(params.Args) > 0 {
		second, hasSecond = params.Args[0].Float(), true
	} else if kw, ok := params.KwArgs["second"]; ok && !kw.IsNil() {
		second, hasSecond = kw.Float(), true
	}

	utc := false
	if len(params.Args) > 1 {
		utc = params.Args[1].IsTrue()
	} else if kw, ok := params.KwArgs["utc"]; ok {
		utc = kw.IsTrue()
	}

	var t time.Time
	if hasSecond {
		sec := int64(second)
		nsec := int64((second - float64(sec)) * 1e9)
		t = time.Unix(sec, nsec)
	} else {
		t = time.Now()
	}
	if utc {
		t = t.UTC()
	} else {
		t = t.Local()
	}

	layout, err := strftimeToGoLayout(format)
	if err != nil {
		return exec.ValueError(fmt.Errorf("strftime: %w", err))
	}
	return exec.AsValue(t.Format(layout))
}

// passwordHashAlgo mirrors one entry of real Ansible's own
// ansible.utils.encrypt.BaseHash.algorithms table exactly (crypt_id,
// salt_size, implicit_rounds, salt_exact, implicit_ident — confirmed from
// source): id is the crypt(3)/MCF identifier; saltSize is the max (or, when
// saltExact, the exact) length a caller-supplied salt must have;
// implicitRounds is the rounds (sha256/512) or cost (bcrypt) used when the
// caller doesn't specify one — real Ansible's own default for sha256/512
// (535000/656000) is deliberately far above crypt(3)'s own spec default of
// 5000, so even the "no rounds given" case always prints a "rounds=N$"
// prefix in the output, confirmed against real `openssl passwd`.
type passwordHashAlgo struct {
	id             string
	saltSize       int
	saltExact      bool
	implicitRounds int
	implicitIdent  string
}

var passwordHashAlgorithms = map[string]passwordHashAlgo{
	"md5":          {id: "1", saltSize: 8},
	"md5_crypt":    {id: "1", saltSize: 8},
	"sha256":       {id: "5", saltSize: 16, implicitRounds: 535000},
	"sha256_crypt": {id: "5", saltSize: 16, implicitRounds: 535000},
	"sha512":       {id: "6", saltSize: 16, implicitRounds: 656000},
	"sha512_crypt": {id: "6", saltSize: 16, implicitRounds: 656000},
	"blowfish":     {id: "2b", saltSize: 22, saltExact: true, implicitRounds: 12, implicitIdent: "2b"},
	"bcrypt":       {id: "2b", saltSize: 22, saltExact: true, implicitRounds: 12, implicitIdent: "2b"},
}

// filterPasswordHash ports real Ansible's password_hash filter
// (ansible.utils.encrypt.do_encrypt/get_encrypted_password): crypt(3)/MCF
// password hashing via github.com/go-encryptions/unixcrypt. Real Ansible
// itself defers to whichever of libxcrypt or passlib is installed on the
// controller; this port always uses unixcrypt's own pure-Go implementation,
// so results are portable (byte-identical on every OS/arch) rather than
// depending on what happens to be installed locally — a real, disclosed
// difference in provenance, though the output format and algorithms
// themselves are byte-for-byte the same crypt(3)/MCF standard.
func filterPasswordHash(e *exec.Evaluator, in *exec.Value, params *exec.VarArgs) *exec.Value {
	password := in.String()

	hashtype := "sha512"
	if len(params.Args) > 0 {
		hashtype = params.Args[0].String()
	} else if kw, ok := params.KwArgs["hashtype"]; ok && !kw.IsNil() {
		hashtype = kw.String()
	}
	algo, ok := passwordHashAlgorithms[strings.ToLower(hashtype)]
	if !ok {
		names := make([]string, 0, len(passwordHashAlgorithms))
		for name := range passwordHashAlgorithms {
			names = append(names, name)
		}
		sort.Strings(names)
		return exec.ValueError(fmt.Errorf("password_hash: unsupported hashtype %q, must be one of %s", hashtype, strings.Join(names, ", ")))
	}

	saltArg, hasSalt := "", false
	if len(params.Args) > 1 {
		saltArg, hasSalt = params.Args[1].String(), true
	} else if kw, ok := params.KwArgs["salt"]; ok && !kw.IsNil() {
		saltArg, hasSalt = kw.String(), true
	}
	saltSize := algo.saltSize
	if len(params.Args) > 2 {
		saltSize = params.Args[2].Integer()
	} else if kw, ok := params.KwArgs["salt_size"]; ok && !kw.IsNil() {
		saltSize = kw.Integer()
	}
	rounds, hasRounds := 0, false
	if len(params.Args) > 3 {
		rounds, hasRounds = params.Args[3].Integer(), true
	} else if kw, ok := params.KwArgs["rounds"]; ok && !kw.IsNil() {
		rounds, hasRounds = kw.Integer(), true
	}
	ident := algo.implicitIdent
	if len(params.Args) > 4 {
		ident = params.Args[4].String()
	} else if kw, ok := params.KwArgs["ident"]; ok && !kw.IsNil() {
		ident = kw.String()
	}

	if algo.id == "2b" {
		return passwordHashBcrypt(password, ident, algo, saltArg, hasSalt, rounds, hasRounds)
	}

	salt := saltArg
	if !hasSalt {
		s, err := unixcrypt.RandomSalt(saltSize)
		if err != nil {
			return exec.ValueError(fmt.Errorf("password_hash: %w", err))
		}
		salt = s
	} else if v := validatePasswordHashSalt(salt, algo); v != nil {
		return v
	}

	switch algo.id {
	case "1":
		return exec.AsValue(unixcrypt.MD5Crypt(password, salt))
	case "5":
		r := algo.implicitRounds
		if hasRounds {
			r = rounds
		}
		return exec.AsValue(unixcrypt.SHA256Crypt(password, salt, r))
	default: // "6"
		r := algo.implicitRounds
		if hasRounds {
			r = rounds
		}
		return exec.AsValue(unixcrypt.SHA512Crypt(password, salt, r))
	}
}

// validatePasswordHashSalt ports CryptHash._salt's own validation (invalid
// characters; too long for a variable-length salt; wrong length for an
// exact-length one) — real Ansible errors on an oversized salt rather than
// silently truncating it the way the bare crypt(3) algorithm itself would.
func validatePasswordHashSalt(salt string, algo passwordHashAlgo) *exec.Value {
	if !unixcrypt.ValidSaltChars(salt) {
		return exec.ValueError(fmt.Errorf("password_hash: invalid characters in salt"))
	}
	if algo.saltExact && len(salt) != algo.saltSize {
		return exec.ValueError(fmt.Errorf("password_hash: invalid salt size supplied (%d), expected %d", len(salt), algo.saltSize))
	}
	if !algo.saltExact && len(salt) > algo.saltSize {
		return exec.ValueError(fmt.Errorf("password_hash: invalid salt size supplied (%d), expected at most %d", len(salt), algo.saltSize))
	}
	return nil
}

// passwordHashBcrypt handles the bcrypt/blowfish hashtype: its salt is 16
// raw bytes, not a crypt(3)-charset string, and its "rounds" argument is
// really the cost (work) factor. A fresh salt is always generated as
// exactly 16 raw random bytes — bcrypt's own fixed algorithm requirement —
// a disclosed simplification of real Ansible's own salt_size-overridable
// fresh-salt generation, which has no well-defined meaning for a byte count
// other than 16 here anyway. A caller-supplied salt is expected as
// bcrypt's own 22-character base64-like encoding (what any existing
// bcrypt hash's own salt field already looks like); it's reassembled into
// an MCF string and decoded via unixcrypt.BcryptFromMCF.
func passwordHashBcrypt(password, ident string, algo passwordHashAlgo, saltArg string, hasSalt bool, rounds int, hasRounds bool) *exec.Value {
	cost := algo.implicitRounds
	if hasRounds {
		cost = rounds
	}

	if !hasSalt {
		salt := make([]byte, 16)
		if _, err := rand.Read(salt); err != nil {
			return exec.ValueError(fmt.Errorf("password_hash: %w", err))
		}
		hashed, err := unixcrypt.Bcrypt(password, ident, cost, salt)
		if err != nil {
			return exec.ValueError(fmt.Errorf("password_hash: %w", err))
		}
		return exec.AsValue(hashed)
	}

	if v := validatePasswordHashSalt(saltArg, algo); v != nil {
		return v
	}
	mcf := fmt.Sprintf("%02d$%s", cost, saltArg)
	hashed, err := unixcrypt.BcryptFromMCF(password, ident, mcf)
	if err != nil {
		return exec.ValueError(fmt.Errorf("password_hash: %w", err))
	}
	return exec.AsValue(hashed)
}
