package template

import "testing"

func BenchmarkEval(b *testing.B) {
	e := New()
	data := map[string]any{"a": 1, "s": "x"}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, err := e.Eval("a + 1 > 0 and s == 'x'", data); err != nil {
			b.Fatal(err)
		}
	}
}
