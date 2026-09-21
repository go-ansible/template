package template

import (
	"reflect"
	"testing"
)

// TestRawStringLiteralsMatchRealAnsible pins Jinja string-literal
// semantics to values measured from real ansible-core 2.21.4, which
// treats a literal as RAW: a backslash is an ordinary character.
//
// This is a deliberate divergence from plain Jinja2 (3.1.6), where
// 'C:\Users' is an ERROR — "truncated \UXXXXXXXX escape" — and "x\ny"
// is three characters. Real Ansible answers C:\Users and four. Both
// were run to check, because the two disagree and only one of them is
// the target.
func TestRawStringLiteralsMatchRealAnsible(t *testing.T) {
	e := New()
	tests := []struct {
		expr string
		want any
	}{
		// A Windows path parses, and keeps its backslashes.
		{`'C:\Users\foo'`, `C:\Users\foo`},
		{`"C:\Users" | length`, 8},
		// A backslash escape is NOT interpreted: these are four
		// characters, not three.
		{`"x\ny" | length`, 4},
		{`"x\ty" | length`, 4},
		// Doubling a backslash yields TWO backslashes, which is why a
		// regex written with doubled escapes stops matching.
		{`"a\\b" | length`, 4},
		// The regex cases this breaks in practice.
		{`"a.b" | regex_replace("\.", "-")`, "a-b"},
		{`"a1b" | regex_replace("\d", "#")`, "a#b"},
		{`"foo123" | regex_replace("foo(\d+)", "bar\1")`, "bar123"},
		// ...and their doubled forms, which real Ansible does NOT match.
		{`"foo123" | regex_replace("foo(\\d+)", "bar\\1")`, "foo123"},
		{`"hello world" | regex_search("w\w+")`, "world"},
		{`"hello world" | regex_search("w\\w+")`, nil},
		// A quote escape keeps BOTH the backslash and the quote, while
		// still not ending the literal.
		{`"a\"b" | length`, 4},
		{`'a\'b' | length`, 4},
		// A UNC path survives intact.
		{`"\\server\share" | length`, 14},
		// Literals without backslashes are untouched by any of this.
		{`"plain"`, "plain"},
		{`'it is' | length`, 5},
	}
	for _, tt := range tests {
		t.Run(tt.expr, func(t *testing.T) {
			got, err := e.Eval(tt.expr, nil)
			if err != nil {
				t.Fatalf("Eval(%s): %v", tt.expr, err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Eval(%s) = %#v, real ansible-core gives %#v", tt.expr, got, tt.want)
			}
		})
	}
}

// The same semantics through Render, which is a different entry point.
func TestRawStringLiteralsInRender(t *testing.T) {
	e := New()
	tests := []struct{ src, want string }{
		{`{{ 'C:\Users\foo' }}`, `C:\Users\foo`},
		{`{{ "a.b" | regex_replace("\.", "-") }}`, "a-b"},
		// Text OUTSIDE a Jinja block is not touched, backslashes included.
		{`a\b {{ 1 + 1 }} c\d`, `a\b 2 c\d`},
		{`C:\Users`, `C:\Users`},
		// A literal containing the block delimiters must not end the
		// block early.
		{`{{ "}}" }}`, `}}`},
		{`{{ "a\}}b" | length }}`, "5"},
	}
	for _, tt := range tests {
		t.Run(tt.src, func(t *testing.T) {
			got, err := e.Render(tt.src, nil)
			if err != nil {
				t.Fatalf("Render(%s): %v", tt.src, err)
			}
			if got != tt.want {
				t.Errorf("Render(%s) = %q, want %q", tt.src, got, tt.want)
			}
		})
	}
}

// A template that mentions the lifting prefix is left alone entirely,
// so a playbook cannot collide with the variables this injects.
func TestRawLiteralPrefixIsNotRewritten(t *testing.T) {
	src := `{{ '\x' }}` + rawLiteralPrefix
	got, consts := liftRawStringLiterals(src)
	if got != src || consts != nil {
		t.Errorf("a source mentioning the prefix must be left alone, got %q / %v", got, consts)
	}
}

func TestLiftOnlyTouchesBackslashLiterals(t *testing.T) {
	// Nothing to do: no backslash anywhere.
	src := `{{ 'plain' | upper }}`
	got, consts := liftRawStringLiterals(src)
	if got != src || consts != nil {
		t.Errorf("untouched source was rewritten: %q / %v", got, consts)
	}
	// One literal has a backslash, the other does not.
	got, consts = liftRawStringLiterals(`{{ 'keep' ~ 'a\b' }}`)
	if len(consts) != 1 {
		t.Fatalf("want exactly one lifted literal, got %v", consts)
	}
	for _, v := range consts {
		if v != `a\b` {
			t.Errorf("lifted value = %#v, want %q", v, `a\b`)
		}
	}
	if !contains(got, `'keep'`) {
		t.Errorf("the backslash-free literal must stay inline: %q", got)
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (func() bool {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	})()
}

// A "}}" inside a string literal does not end the block, so an
// expression containing one is still a whole expression and keeps its
// native type. Real ansible-core answers 5, not "5".
func TestWholeExpressionSkipsLiterals(t *testing.T) {
	e := New()
	tests := []struct {
		src  string
		want any
	}{
		{`{{ "a\}}b" | length }}`, 5},
		{`{{ "a}}b" | length }}`, 4},
		{`{{ "}}" }}`, "}}"},
		{`{{ 1 + 1 }}`, 2},
		// Two real blocks are still not a whole expression: the result
		// is the rendered string.
		{`{{ 1 }}{{ 2 }}`, "12"},
		{`x {{ 1 }}`, "x 1"},
	}
	for _, tt := range tests {
		t.Run(tt.src, func(t *testing.T) {
			got, err := e.RenderValue(tt.src, nil)
			if err != nil {
				t.Fatalf("RenderValue(%s): %v", tt.src, err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("RenderValue(%s) = %#v, real ansible-core gives %#v", tt.src, got, tt.want)
			}
		})
	}
}

// TestTwoStringLiteralModes pins the fact that real ansible-core 2.21
// uses BOTH semantics, depending on where the expression was written.
// One expression, two places, two answers — measured:
//
//	{{ "x\ny" | length }}   inline in a playbook   4
//	{{ "x\ny" | length }}   inside a .j2 file      3
//
// So this is not a question of which is "right": the port needs both,
// and picks by caller.
func TestTwoStringLiteralModes(t *testing.T) {
	tests := []struct {
		expr      string
		inline    any // Ansible's raw literals
		inAFile   any // plain Jinja2's escapes
		fileFails bool
	}{
		{expr: `"x\ny" | length`, inline: 4, inAFile: 3},
		{expr: `"x\ty" | length`, inline: 4, inAFile: 3},
		// A Windows path parses raw and is an error under Jinja2, which
		// is what real Ansible reports for a .j2 containing one.
		{expr: `"C:\Users"`, inline: `C:\Users`, fileFails: true},
		// Without a backslash the two agree, which is the common case.
		{expr: `"plain" | length`, inline: 5, inAFile: 5},
		{expr: `"a.b" | regex_replace("x", "-")`, inline: "a.b", inAFile: "a.b"},
	}

	for _, tt := range tests {
		t.Run(tt.expr, func(t *testing.T) {
			got, err := New().Eval(tt.expr, nil)
			if err != nil {
				t.Fatalf("inline: %v", err)
			}
			if got != tt.inline {
				t.Errorf("inline = %#v, real ansible-core gives %#v", got, tt.inline)
			}

			got, err = New().JinjaStringEscapes().Eval(tt.expr, nil)
			if tt.fileFails {
				if err == nil {
					t.Errorf("in a file this must fail as Jinja2 does, got %#v", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("in a file: %v", err)
			}
			if got != tt.inAFile {
				t.Errorf("in a file = %#v, real Jinja2 gives %#v", got, tt.inAFile)
			}
		})
	}
}
