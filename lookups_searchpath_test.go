package template

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The order was measured against real ansible-core 2.21.4 with the SAME
// filename present in four places at once, removing the winner each
// time: <role>/files, then <role>, then <playbook_dir>/files, then
// <playbook_dir>. The working directory never wins, and with the file
// only there real fails outright.
func TestFileLookupSearchPathOrder(t *testing.T) {
	root := t.TempDir()
	role := filepath.Join(root, "roles", "r1")
	pb := filepath.Join(root, "pb")
	for _, d := range []string{
		filepath.Join(role, "files"), filepath.Join(role, "tasks"),
		filepath.Join(pb, "files"),
	} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	write := func(dir, content string) string {
		p := filepath.Join(dir, "sp.txt")
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	// Exactly the search path the engine builds for a role task.
	vars := map[string]any{"ansible_search_path": []any{
		role, filepath.Join(role, "tasks"), pb,
	}}

	layers := []struct {
		dir, content string
	}{
		{filepath.Join(role, "files"), "ROLE_FILES"},
		{role, "ROLE_DIR"},
		{filepath.Join(pb, "files"), "PB_FILES"},
		{pb, "PLAYBOOK"},
	}
	for _, l := range layers {
		write(l.dir, l.content)
	}
	for _, l := range layers {
		got, err := lookupFile([]any{"sp.txt"}, vars, nil)
		if err != nil {
			t.Fatalf("expected %s: %v", l.content, err)
		}
		if got[0] != l.content {
			t.Fatalf("got %v, want %v", got[0], l.content)
		}
		// Remove this layer and the next one down must win.
		if err := os.Remove(filepath.Join(l.dir, "sp.txt")); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := lookupFile([]any{"sp.txt"}, vars, nil); err == nil {
		t.Error("found the file with every candidate removed")
	}
}

func TestFileLookupDoesNotSearchTheWorkingDirectory(t *testing.T) {
	// Real fails when the file exists only in the working directory.
	// This port used to read it, which is the same defect seen from the
	// other side: it also could not find the ones real does.
	root := t.TempDir()
	pb := filepath.Join(root, "pb")
	if err := os.MkdirAll(pb, 0o755); err != nil {
		t.Fatal(err)
	}
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Chdir(root)
	defer func() { _ = os.Chdir(cwd) }()
	if err := os.WriteFile(filepath.Join(root, "only-here.txt"), []byte("CWD"), 0o644); err != nil {
		t.Fatal(err)
	}
	vars := map[string]any{"ansible_search_path": []any{pb}}
	if got, err := lookupFile([]any{"only-here.txt"}, vars, nil); err == nil {
		t.Errorf("read %v from the working directory; real does not search it", got)
	}
}

func TestFileLookupWithoutASearchPathUsesTheNameAsGiven(t *testing.T) {
	// This package is usable on its own, outside a playbook, and then
	// there is nothing else to resolve against.
	dir := t.TempDir()
	p := filepath.Join(dir, "plain.txt")
	if err := os.WriteFile(p, []byte("here"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := lookupFile([]any{p}, map[string]any{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got[0] != "here" {
		t.Errorf("got %v", got[0])
	}
}

func TestFileLookupAbsolutePathIgnoresTheSearchPath(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "abs.txt")
	if err := os.WriteFile(p, []byte("abs"), 0o644); err != nil {
		t.Fatal(err)
	}
	vars := map[string]any{"ansible_search_path": []any{t.TempDir()}}
	got, err := lookupFile([]any{p}, vars, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got[0] != "abs" {
		t.Errorf("got %v", got[0])
	}
}

func TestFileLookupMissingFileUsesRealsWording(t *testing.T) {
	// Measured: Unable to access the file 'nope.txt': File not found.
	// Use -vvvvv to see paths searched.
	_, err := lookupFile([]any{"nope.txt"}, map[string]any{
		"ansible_search_path": []any{t.TempDir()},
	}, nil)
	if err == nil {
		t.Fatal("no error")
	}
	want := `Unable to access the file 'nope.txt': File not found. Use -vvvvv to see paths searched.`
	if err.Error() != want {
		t.Errorf("err = %q\nwant %q", err.Error(), want)
	}
}

// A strict lookup failure names the plugin the way real does. The
// prefix is load-bearing: the playbook engine finds the part real would
// have printed by looking for it, because gonja wraps a global
// function's error in several layers of its own prose.
func TestStrictLookupFailureUsesRealsPrefix(t *testing.T) {
	e := New()
	_, err := e.Eval(`lookup('file', 'definitely-not-here.txt')`, map[string]any{
		"ansible_search_path": []any{t.TempDir()},
	})
	if err == nil {
		t.Fatal("no error")
	}
	const want = "The lookup plugin 'file' failed: Unable to access the file " +
		"'definitely-not-here.txt': File not found. Use -vvvvv to see paths searched."
	if !strings.Contains(err.Error(), want) {
		t.Errorf("err = %q\ndoes not contain %q", err.Error(), want)
	}
}
