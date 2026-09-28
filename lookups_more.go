package template

import (
	"crypto/rand"
	"errors"
	"fmt"
	"math/big"
	"os"
	osexec "os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// registerEngineLookups installs the built-in lookups that need the
// Engine itself. They cannot go in registerLookups, which runs before
// the Engine exists, so New() calls this once the Engine is built.
func (e *Engine) registerEngineLookups() {
	e.lookups["vars"] = e.lookupVars
}

// errUndefinedVar marks a lookup failure that real Ansible reports as an
// UNDEFINED VALUE rather than as a plugin failure. The distinction is
// visible: measured against ansible-core 2.21.4, a missing name gives
//
//	Error while resolving value for 'msg': No variable named 'x' was found.
//
// with no "The lookup plugin 'vars' failed" in front of it, while a
// non-string term gives
//
//	... The lookup plugin 'vars' failed: Variable name must be 'str' not 'int'.
//
// because real's vars lookup returns an undefined marker in the first
// case and raises in the second. This port has no undefined marker to
// hand back through gonja, so it carries the difference in the error
// type instead and the dispatcher leaves the prefix off.
type errUndefinedVar struct{ msg string }

func (e errUndefinedVar) Error() string { return e.msg }

// lookupVars ports real Ansible's vars lookup plugin: the value of each
// named variable, TEMPLATED — real ends with
// `self._templar._engine.template(ret)`, so a variable holding
// "value is {{ 1 + 1 }}" comes back as "value is 2" and not as its own
// source.
//
// A name that is not in scope yields the `default` option; with no
// default real yields an undefined value, which errors where it is used
// (see errUndefinedVar). A non-string term is an error either way.
func (e *Engine) lookupVars(terms []any, variables map[string]any, kwargs map[string]any) ([]any, error) {
	def, hasDefault := kwargs["default"]

	out := make([]any, 0, len(terms))
	for _, term := range terms {
		name, ok := term.(string)
		if !ok {
			// Real names the two types with Python's own spellings, so
			// its message is reproduced with the Python name for the Go
			// type rather than the Go one.
			return nil, fmt.Errorf("Variable name must be %s not %s.", pythonQuote(pythonTypeName("")), pythonQuote(pythonTypeName(term)))
		}
		value, present := variables[name]
		if !present {
			if !hasDefault {
				return nil, errUndefinedVar{fmt.Sprintf("No variable named %s was found.", pythonQuote(name))}
			}
			value = def
		}
		rendered, err := e.RenderValue(value, variables)
		if err != nil {
			return nil, err
		}
		out = append(out, rendered)
	}
	return out, nil
}

// lookupVarnames ports real Ansible's varnames lookup plugin: every
// variable NAME matching each term read as a regular expression.
//
// Three measured details a simpler reading would miss. The match is
// re.SEARCH, so an unanchored term matches anywhere in the name.
// Duplicates are KEPT: a name matched by two terms is listed twice, in
// term order. And the result is the names, never the values.
//
// The error text for an unusable pattern is Go's, not Python's: real
// reports CPython's own re diagnostic ("unterminated character set at
// position 0") and reproducing those verbatim would mean carrying a
// second regexp implementation's prose. The sentence around it is
// real's.
func lookupVarnames(terms []any, variables map[string]any, _ map[string]any) ([]any, error) {
	names := sortedVarNames(variables)

	var out []any
	for _, term := range terms {
		pattern, ok := term.(string)
		if !ok {
			return nil, fmt.Errorf("Invalid setting identifier, \"%v\" is not a string, it is a <class %s>", term, pythonQuote(pythonTypeName(term)))
		}
		re, err := regexp.Compile(pattern)
		if err != nil {
			return nil, fmt.Errorf("Unable to use \"%s\" as a search parameter: %v", pattern, err)
		}
		for _, name := range names {
			if re.MatchString(name) {
				out = append(out, name)
			}
		}
	}
	if out == nil {
		out = []any{}
	}
	return out, nil
}

// lookupLines ports real Ansible's lines lookup plugin: run each term
// through a shell on the controller and return its stdout SPLIT INTO
// LINES, which is the whole difference from pipe.
//
// Measured: the split is on newlines only — a tab survives — a blank
// line in the middle is kept as an empty string, and a trailing newline
// does not produce a trailing empty element. Empty output is an empty
// list, not a list holding one empty string.
func lookupLines(terms []any, _ map[string]any, _ map[string]any) ([]any, error) {
	var out []any
	for _, term := range terms {
		command := fmt.Sprintf("%v", term)
		stdout, err := osexec.Command("sh", "-c", command).Output()
		if err != nil {
			var exitErr *osexec.ExitError
			if errors.As(err, &exitErr) {
				return nil, fmt.Errorf("lookup_plugin.lines(%s) returned %d", command, exitErr.ExitCode())
			}
			return nil, fmt.Errorf("lookup_plugin.lines(%s): %w", command, err)
		}
		text := string(stdout)
		// Python's splitlines on an EMPTY string gives [], not [""] --
		// so an empty stdout contributes nothing at all.
		if text == "" {
			continue
		}
		text = strings.TrimSuffix(text, "\n")
		for _, line := range strings.Split(text, "\n") {
			out = append(out, line)
		}
	}
	if out == nil {
		out = []any{}
	}
	return out, nil
}

// lookupRandomChoice ports real Ansible's random_choice lookup plugin:
// ONE of the terms, chosen with a cryptographic source (real uses
// secrets.choice, not random.choice).
//
// Measured: the result is always a list of one, an empty term list comes
// back empty rather than erroring, and a term is returned as it was
// given -- a number stays a number.
func lookupRandomChoice(terms []any, _ map[string]any, _ map[string]any) ([]any, error) {
	if len(terms) == 0 {
		return []any{}, nil
	}
	n, err := rand.Int(rand.Reader, big.NewInt(int64(len(terms))))
	if err != nil {
		return nil, fmt.Errorf("Unable to choose random term: %v", err)
	}
	return []any{terms[n.Int64()]}, nil
}

// lookupFileglob ports real Ansible's fileglob lookup plugin: the files
// matching each glob term, as ABSOLUTE paths.
//
// Measured against ansible-core 2.21.4:
//
//   - directories are excluded, so a glob matching only a directory
//     returns nothing;
//   - the search STOPS at the first directory that yields any match. A
//     file present in both <basedir>/files and <basedir> is found once,
//     under files, and the second copy is never reported;
//   - a term with a directory component resolves that directory through
//     the task's search path first, and only its basename is globbed.
//
// One disclosed difference: real returns CPython glob's order, which is
// the filesystem's; Go's filepath.Glob sorts. Sorted is a superset of
// deterministic, so nothing real produces is lost -- but a caller
// comparing the two must sort, which is what a corpus case here does.
func lookupFileglob(terms []any, variables map[string]any, _ map[string]any) ([]any, error) {
	var out []any
	for _, term := range terms {
		pattern := fmt.Sprintf("%v", term)
		base := filepath.Base(pattern)

		var dirs []string
		if dir := filepath.Dir(pattern); base != pattern {
			// A term naming a directory: real resolves THAT directory
			// through the search path and globs inside it alone.
			if resolved, ok := findDirInSearchPath(variables, "files", dir); ok {
				dirs = append(dirs, resolved)
			}
		} else {
			for _, p := range searchPathDirs(variables) {
				dirs = append(dirs, filepath.Join(p, "files"), p)
			}
			if len(dirs) == 0 {
				dirs = append(dirs, "files", ".")
			}
		}

		for _, dir := range dirs {
			matches, err := filepath.Glob(filepath.Join(dir, base))
			if err != nil {
				return nil, fmt.Errorf("Unable to use \"%s\" as a glob: %v", pattern, err)
			}
			var found []any
			for _, m := range matches {
				if !fileExists(m) { // excludes directories, as real does
					continue
				}
				abs, err := filepath.Abs(m)
				if err != nil {
					abs = m
				}
				found = append(found, abs)
			}
			if len(found) > 0 {
				out = append(out, found...)
				break
			}
		}
	}
	if out == nil {
		out = []any{}
	}
	return out, nil
}

// findDirInSearchPath is findInSearchPath for a DIRECTORY rather than a
// file: same <dir>/<subdir>/<name> then <dir>/<name> order, same
// first-hit-wins rule, but the candidate has to be a directory.
func findDirInSearchPath(variables map[string]any, subdir, name string) (string, bool) {
	if filepath.IsAbs(name) {
		return name, dirExists(name)
	}
	dirs := searchPathDirs(variables)
	if len(dirs) == 0 {
		return name, dirExists(name)
	}
	for _, dir := range dirs {
		for _, candidate := range []string{
			filepath.Join(dir, subdir, name),
			filepath.Join(dir, name),
		} {
			if dirExists(candidate) {
				return candidate, true
			}
		}
	}
	return "", false
}

// sortedVarNames lists the variable names in a stable order. Real walks
// its own dict, whose order is how the variables were assembled; a Go
// map has none, so the names are sorted. The ORDER of varnames' result
// is therefore this port's, not real's -- a caller comparing them sorts,
// and the SET is the same either way.
func sortedVarNames(variables map[string]any) []string {
	out := make([]string, 0, len(variables))
	for name := range variables {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// dirExists is fileExists' counterpart: a path that is there AND is a
// directory.
func dirExists(path string) bool {
	st, err := os.Stat(path)
	return err == nil && st.IsDir()
}
