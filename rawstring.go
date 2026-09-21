package template

import (
	"fmt"
	"strings"
)

// rawLiteralPrefix names the variables raw string literals are lifted
// into. The trailing and leading underscores make an accidental clash
// with a playbook's own variable essentially impossible, and a source
// that somehow contains the prefix already is left alone entirely
// rather than rewritten around.
const rawLiteralPrefix = "__go_ansible_raw_"

// liftRawStringLiterals rewrites every string literal CONTAINING A
// BACKSLASH into a variable reference, and returns the values those
// variables must hold.
//
// Why this exists: real ansible-core 2.21 treats a Jinja string literal
// as RAW — a backslash is an ordinary character, and `\'` or `\"` keeps
// both the backslash and the quote while not ending the literal. Plain
// Jinja2 does not (it applies Python's unicode-escape, where
// 'C:\Users' is an error), and gonja does something third: it re-quotes
// the lexed value and then un-escapes what it just escaped, so
//
//	'C:\Users'            fails to parse outright
//	"x\ny" | length       quietly answers 3 where real answers 4
//
// Both are wrong, and the silent one is worse. Every measurement says
// the value real Ansible produces is the source text between the
// quotes, byte for byte — `\.` stays `\.`, `a\"b` stays `a\"b` — so
// lifting the literal out to a variable whose value IS that text
// reproduces it exactly, without asking gonja to parse an escape at
// all.
//
// Only literals containing a backslash are touched. Anything that
// parses correctly today keeps its existing path, so this cannot
// change a template that already works.
func liftRawStringLiterals(src string) (string, map[string]any) {
	if !strings.Contains(src, `\`) || strings.Contains(src, rawLiteralPrefix) {
		return src, nil
	}

	var out strings.Builder
	out.Grow(len(src))
	var consts map[string]any

	for i := 0; i < len(src); {
		// Outside a Jinja block there are no expressions, so nothing to
		// rewrite: a backslash in ordinary template text is just text.
		start, end, ok := nextJinjaBlock(src, i)
		if !ok {
			out.WriteString(src[i:])
			break
		}
		out.WriteString(src[i:start])

		block := src[start:end]
		rewritten, blockConsts := liftInBlock(block, len(consts))
		out.WriteString(rewritten)
		for k, v := range blockConsts {
			if consts == nil {
				consts = map[string]any{}
			}
			consts[k] = v
		}
		i = end
	}
	return out.String(), consts
}

// nextJinjaBlock finds the next {{ … }}, {% … %} or {# … #} at or after
// i, returning its bounds. Literals only occur inside one.
func nextJinjaBlock(src string, i int) (start, end int, ok bool) {
	for j := i; j+1 < len(src); j++ {
		if src[j] != '{' {
			continue
		}
		var closing string
		switch src[j+1] {
		case '{':
			closing = "}}"
		case '%':
			closing = "%}"
		case '#':
			closing = "#}"
		default:
			continue
		}
		// An unterminated block is left for gonja to complain about,
		// with its own message and position.
		k := indexOutsideLiteral(src[j+2:], closing)
		if k < 0 {
			return 0, 0, false
		}
		return j, j + 2 + k + len(closing), true
	}
	return 0, 0, false
}

// indexOutsideLiteral finds sep in s, skipping over string literals so
// a closing delimiter inside one ("}}" in a regex, say) does not end
// the block early.
func indexOutsideLiteral(s, sep string) int {
	for i := 0; i < len(s); {
		if s[i] == '\'' || s[i] == '"' {
			i = skipLiteral(s, i)
			continue
		}
		if strings.HasPrefix(s[i:], sep) {
			return i
		}
		i++
	}
	return -1
}

// skipLiteral returns the index just past the literal starting at i.
// A quote preceded by a backslash does not end it — real Ansible's own
// rule, and gonja's lexer applies the same one.
func skipLiteral(s string, i int) int {
	quote := s[i]
	for j := i + 1; j < len(s); j++ {
		if s[j] == '\\' {
			j++ // whatever follows is part of the literal, quote included
			continue
		}
		if s[j] == quote {
			return j + 1
		}
	}
	return len(s) // unterminated; gonja reports it
}

// liftInBlock rewrites the literals of one Jinja block.
func liftInBlock(block string, startIndex int) (string, map[string]any) {
	var out strings.Builder
	out.Grow(len(block))
	var consts map[string]any
	n := startIndex

	for i := 0; i < len(block); {
		if block[i] != '\'' && block[i] != '"' {
			out.WriteByte(block[i])
			i++
			continue
		}
		end := skipLiteral(block, i)
		literal := block[i:end]
		// The content between the quotes IS the value; an unterminated
		// literal is left alone for gonja to report.
		if len(literal) < 2 || literal[len(literal)-1] != literal[0] {
			out.WriteString(literal)
			i = end
			continue
		}
		content := literal[1 : len(literal)-1]
		if !strings.Contains(content, `\`) {
			out.WriteString(literal)
			i = end
			continue
		}

		name := fmt.Sprintf("%s%d__", rawLiteralPrefix, n)
		n++
		if consts == nil {
			consts = map[string]any{}
		}
		consts[name] = content
		out.WriteString(name)
		i = end
	}
	return out.String(), consts
}

// withRawLiterals merges the lifted values into a copy of data, leaving
// the caller's map untouched.
func withRawLiterals(data map[string]any, consts map[string]any) map[string]any {
	if len(consts) == 0 {
		return data
	}
	merged := make(map[string]any, len(data)+len(consts))
	for k, v := range data {
		merged[k] = v
	}
	for k, v := range consts {
		merged[k] = v
	}
	return merged
}
