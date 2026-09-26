package template

import (
	"errors"
	"fmt"
	"os"
	osexec "os/exec"
	"path/filepath"
	"strings"
	"unicode"
)

// lookupEnv ports real Ansible's env lookup plugin: the value of each
// named environment variable on the controller, or the `default` option
// (real default: the empty string) when one is unset.
//
// Real Ansible takes only the first whitespace-separated word of each
// term (`var = term.split()[0]`), which on an empty term raises an
// IndexError — a real crash this port declines to reproduce, treating an
// empty term as an unset variable instead.
func lookupEnv(terms []any, _ map[string]any, kwargs map[string]any) ([]any, error) {
	var def any = ""
	if d, ok := kwargs["default"]; ok {
		def = d
	}
	out := make([]any, 0, len(terms))
	for _, term := range terms {
		name := fmt.Sprintf("%v", term)
		if fields := strings.Fields(name); len(fields) > 0 {
			name = fields[0]
		} else {
			name = ""
		}
		if v, ok := os.LookupEnv(name); ok {
			out = append(out, v)
			continue
		}
		out = append(out, def)
	}
	return out, nil
}

// lookupPipe ports real Ansible's pipe lookup plugin: run each term
// through a shell ON THE CONTROLLER (never the target host — real
// Ansible's own note, and the reason become/delegation never affect it)
// and return its stdout with trailing whitespace removed.
//
// Real Ansible runs each command with cwd set to the play's own basedir;
// this port uses the process's own working directory, since go-ansible
// has no per-play basedir threaded into templating — a disclosed
// difference for a relative-path command, not for anything else.
func lookupPipe(terms []any, _ map[string]any, _ map[string]any) ([]any, error) {
	out := make([]any, 0, len(terms))
	for _, term := range terms {
		command := fmt.Sprintf("%v", term)
		stdout, err := osexec.Command("sh", "-c", command).Output()
		if err != nil {
			var exitErr *osexec.ExitError
			if errors.As(err, &exitErr) {
				return nil, fmt.Errorf("lookup_plugin.pipe(%s) returned %d", command, exitErr.ExitCode())
			}
			return nil, fmt.Errorf("lookup_plugin.pipe(%s): %w", command, err)
		}
		out = append(out, strings.TrimRightFunc(string(stdout), unicode.IsSpace))
	}
	return out, nil
}

// lookupFile ports real Ansible's file lookup plugin: the contents of
// each named file on the controller, with the rstrip/lstrip options
// (real defaults: rstrip true, lstrip false).
//
// Real Ansible resolves a relative term through the task's own search
// path (`ansible_search_path`/`role_path` in variables, so a bare
// "foo.txt" finds a role's files/foo.txt). go-ansible has no role-file
// search path at all yet, so a relative term here resolves against the
// process's working directory only — a real, disclosed gap rather than a
// silently different resolution.
func lookupFile(terms []any, variables map[string]any, kwargs map[string]any) ([]any, error) {
	rstrip := kwargBool(kwargs, "rstrip", true)
	lstrip := kwargBool(kwargs, "lstrip", false)

	out := make([]any, 0, len(terms))
	for _, term := range terms {
		name := fmt.Sprintf("%v", term)
		path, ok := findInSearchPath(variables, "files", name)
		if !ok {
			// Real's own wording, so a playbook that greps its output
			// for this reads the same thing either way.
			return nil, fmt.Errorf("Unable to access the file '%s': File not found. Use -vvvvv to see paths searched.", name)
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("unable to access the file %q: %w", path, err)
		}
		text := string(content)
		if lstrip {
			text = strings.TrimLeftFunc(text, unicode.IsSpace)
		}
		if rstrip {
			text = strings.TrimRightFunc(text, unicode.IsSpace)
		}
		out = append(out, text)
	}
	return out, nil
}

// findInSearchPath resolves a relative lookup argument the way real's
// LookupBase.find_file_in_search_path does: for each directory in the
// task's ansible_search_path, try <dir>/<subdir>/<name> and then
// <dir>/<name>, first hit wins.
//
// Measured against real for a task inside a role, with the same file in
// four places at once: <role>/files, then <role>, then
// <playbook_dir>/files, then <playbook_dir>. The WORKING DIRECTORY is
// not searched -- with the file present only there, real fails. This
// port used to resolve against the working directory and nothing else,
// so it read files real would not and missed the ones real would.
//
// With no search path in scope -- this package used on its own, outside
// a playbook -- the name is used as given, which is the only thing left
// to do and what the package did before.
func findInSearchPath(variables map[string]any, subdir, name string) (string, bool) {
	if filepath.IsAbs(name) {
		return name, fileExists(name)
	}
	dirs := searchPathDirs(variables)
	if len(dirs) == 0 {
		return name, fileExists(name)
	}
	for _, dir := range dirs {
		for _, candidate := range []string{
			filepath.Join(dir, subdir, name),
			filepath.Join(dir, name),
		} {
			if fileExists(candidate) {
				return candidate, true
			}
		}
	}
	return "", false
}

// searchPathDirs reads ansible_search_path, which the playbook engine
// sets per task. It tolerates the shapes a variable can arrive in --
// []any is what comes back through a template context, []string what a
// Go caller would write.
func searchPathDirs(variables map[string]any) []string {
	var out []string
	switch v := variables["ansible_search_path"].(type) {
	case []any:
		for _, e := range v {
			if s, ok := e.(string); ok && s != "" {
				out = append(out, s)
			}
		}
	case []string:
		for _, s := range v {
			if s != "" {
				out = append(out, s)
			}
		}
	case string:
		if v != "" {
			out = append(out, v)
		}
	}
	return out
}

func fileExists(path string) bool {
	st, err := os.Stat(path)
	return err == nil && !st.IsDir()
}
