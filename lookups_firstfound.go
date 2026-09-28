package template

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// lookupFirstFound ports real Ansible's first_found lookup plugin: the
// first of several candidate files that exists, as a one-element list.
//
// Measured against ansible-core 2.21.4. The rules that a reading of the
// docs would not give:
//
//   - the result is ALWAYS a list of one, or empty — never several;
//   - a term may be a string, a dict of inline options (files, paths,
//     skip) or a list, which is flattened;
//   - with no positional terms at all, the `files` option becomes the
//     terms;
//   - the search is TERM-MAJOR, not path-major. With
//     files=[one.txt, three.txt] and paths=[other, files], real returns
//     one.txt — found under files/ — and not three.txt, which exists
//     under other/ and would win if paths were the outer loop. Real
//     hands the file list to _process_terms as TERMS, so each file is
//     tried against every path before the next file is considered.
//     Only a case where the two orders disagree can tell them apart.
//   - `skip` decides what happens when nothing matched: an empty list
//     rather than an error. A dict term carrying skip sets it for the
//     WHOLE call, not just its own files — real's own note, and a
//     consequence of set_options writing plugin state.
//
// One disclosed gap. Real picks the subdirectory it searches from the
// ENCLOSING TASK's action: `templates` inside a template task, `vars`
// inside one whose action names var, `files` otherwise. This port has
// no channel carrying the action into templating, and inventing a magic
// variable for it would make that variable visible to `varnames` and to
// any vars dump — a divergence in exchange for one. So this always
// searches `files`, which is real's own default for every action that
// names none of the three.
func lookupFirstFound(terms []any, variables map[string]any, kwargs map[string]any) ([]any, error) {
	skip := kwargBool(kwargs, "skip", false)
	defaultPaths := optionList(kwargs["paths"])

	// With nothing positional, the files option IS the term list.
	if len(terms) == 0 {
		terms = anyList(kwargs["files"])
	}

	candidates, skip, err := firstFoundCandidates(terms, defaultPaths, skip)
	if err != nil {
		return nil, err
	}

	for _, candidate := range candidates {
		if path, ok := findInSearchPath(variables, "files", candidate); ok {
			return []any{unfrackPath(path)}, nil
		}
	}
	if skip {
		return []any{}, nil
	}
	return nil, fmt.Errorf("No file was found when using first_found.")
}

// firstFoundCandidates expands the terms into the ordered list of paths
// to try, and returns the skip setting as the last dict term left it.
func firstFoundCandidates(terms []any, defaultPaths []string, skip bool) ([]string, bool, error) {
	var out []string
	for _, term := range terms {
		var files, paths []string

		switch t := term.(type) {
		case string:
			files = splitOn(t, ",;")
			paths = defaultPaths
		case map[string]any:
			files = optionList(t["files"])
			paths = optionListOr(t["paths"], defaultPaths)
			if v, ok := t["skip"]; ok {
				skip = kwargBool(map[string]any{"skip": v}, "skip", skip)
			}
		case []any:
			// A list term is flattened, with the same options in force.
			nested, nestedSkip, err := firstFoundCandidates(t, defaultPaths, skip)
			if err != nil {
				return nil, skip, err
			}
			out = append(out, nested...)
			skip = nestedSkip
			continue
		default:
			// Real's wording, stray closing parenthesis and all.
			return nil, skip, fmt.Errorf("Invalid term supplied. A string, dict or list is required, not %s).", pythonQuote(pythonTypeName(term)))
		}

		if len(paths) == 0 {
			out = append(out, files...)
			continue
		}
		for _, p := range paths {
			for _, f := range files {
				out = append(out, filepath.Join(p, f))
			}
		}
	}
	return out, skip, nil
}

// splitOn reproduces real's _split_on: a string term carrying any of the
// given separators is split on them. Real calls this "magic extra
// splitting" and has deprecated it, but 2.21.4 still does it, so a
// caller writing "a.txt,b.txt" still gets two candidates.
func splitOn(s string, separators string) []string {
	if !strings.ContainsAny(s, separators) {
		return []string{s}
	}
	return strings.FieldsFunc(s, func(r rune) bool {
		return strings.ContainsRune(separators, r)
	})
}

// optionList reads a files/paths option, which may be a list or a single
// string carrying separators.
func optionList(v any) []string {
	switch t := v.(type) {
	case nil:
		return nil
	case string:
		return splitOn(t, ",:;")
	case []any:
		var out []string
		for _, e := range t {
			out = append(out, optionList(e)...)
		}
		return out
	case []string:
		var out []string
		for _, e := range t {
			out = append(out, splitOn(e, ",:;")...)
		}
		return out
	}
	return []string{fmt.Sprintf("%v", v)}
}

// optionListOr is optionList with a fallback for an option the term did
// not name at all — distinct from one it set to an empty list.
func optionListOr(v any, fallback []string) []string {
	if v == nil {
		return fallback
	}
	return optionList(v)
}

// anyList lifts an option back into the []any a term list is made of.
func anyList(v any) []any {
	strs := optionList(v)
	out := make([]any, 0, len(strs))
	for _, s := range strs {
		out = append(out, s)
	}
	return out
}

// unfrackPath is real's unfrackpath(path, follow=False): expand ~, make
// absolute and normalise, WITHOUT resolving symlinks — so a link is
// reported as itself rather than as its target, which is measurable and
// measured.
//
// The normalisation here is a second guarantee rather than the working
// one: findInSearchPath builds its candidates with filepath.Join, which
// already Cleans, so a term walking up and back down arrives collapsed.
// Neutering this function alone does not change the result; neutering
// the Join does. Said plainly because a comment claiming this line is
// what normalises would be false.
func unfrackPath(path string) string {
	if strings.HasPrefix(path, "~") {
		if home, err := os.UserHomeDir(); err == nil {
			path = filepath.Join(home, strings.TrimPrefix(path, "~"))
		}
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return filepath.Clean(path)
	}
	return abs
}
