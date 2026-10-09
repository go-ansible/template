package template

import (
	"runtime"
	"testing"
	"time"

	"github.com/nikolalohinski/gonja/v2/tokens"
)

// goroutinesAfterSettling gives finished goroutines time to exit before
// counting, so a slow exit is not mistaken for a leak.
func goroutinesAfterSettling() int {
	for i := 0; i < 20; i++ {
		runtime.GC()
		time.Sleep(20 * time.Millisecond)
	}
	return runtime.NumGoroutine()
}

const leakProbeCalls = 200

// gonja's tokens.Lex runs the lexer in a goroutine feeding an UNBUFFERED
// channel, so a parse that stops before draining the stream strands that
// goroutine forever -- it is blocked on a send nobody will ever receive.
// evalValue used to do exactly that: ParseExpressionNode stops at the
// closing }}, and on a parse error it returns with tokens still unsent.
// One malformed `when:` expression therefore leaked one goroutine PER
// EVALUATION -- per task, per host -- holding the lexer, its input and a
// token alive with it. The fix is tokens.LexAll, which lexes
// synchronously into a slice and starts no goroutine at all.
//
// The positive control below is not decoration: a leak probe that has
// gone blind reports zero just as loudly as a fixed engine does.

func TestLeakProbeCanSeeALeak(t *testing.T) {
	e := New()
	before := goroutinesAfterSettling()
	const abandoned = 50
	for i := 0; i < abandoned; i++ {
		// Deliberately abandon a stream mid-way: this is the defect
		// being re-created on purpose.
		s := tokens.Lex("{{ aaa + bbb + ccc + ddd + eee + fff + ggg }}", e.cfg)
		s.Next()
	}
	got := goroutinesAfterSettling() - before
	if got < abandoned/2 {
		t.Fatalf("the leak probe is BLIND: abandoned %d lexer streams but counted only %+d goroutines; "+
			"TestEvalDoesNotLeakALexerGoroutine proves nothing while this fails", abandoned, got)
	}
}

func TestEvalDoesNotLeakALexerGoroutine(t *testing.T) {
	e := New()
	data := map[string]any{"a": 1, "s": "x"}

	for _, c := range []struct{ name, expr string }{
		{"valid", "a + 1 > 0"},
		{"error at the very end", "a +"},
		// The one that leaked: it fails EARLY, leaving tokens unsent.
		{"error mid-stream", "a + + + b | nosuchfilter | another"},
		{"undefined name", "nosuchvar"},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, _ = e.Eval(c.expr, data) // warm up
			before := goroutinesAfterSettling()
			for i := 0; i < leakProbeCalls; i++ {
				_, _ = e.Eval(c.expr, data)
			}
			if grew := goroutinesAfterSettling() - before; grew > leakProbeCalls/10 {
				t.Errorf("%d evaluations of %q leaked %+d goroutines", leakProbeCalls, c.expr, grew)
			}
		})
	}
}
