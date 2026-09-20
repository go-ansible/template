package template

import "testing"

// TestToJSONSpacesTypedCollections pins the separator Python's
// json.dumps uses — ", " between items — for collections that are not
// []any / map[string]any. A command module's `cmd` is a []string, and
// real ansible-core prints ["sh", "-c", "exit 1"] where this package
// printed ["sh","-c","exit 1"]: the typed slice fell past the encoder
// into encoding/json, whose output is compact.
func TestToJSONSpacesTypedCollections(t *testing.T) {
	tests := []struct {
		name string
		in   any
		want string
	}{{
		name: "typed string slice",
		in:   []string{"sh", "-c", "exit 1"},
		want: `["sh", "-c", "exit 1"]`,
	}, {
		name: "typed int slice",
		in:   []int{1, 2, 3},
		want: `[1, 2, 3]`,
	}, {
		name: "typed string map",
		in:   map[string]string{"b": "2", "a": "1"},
		want: `{"a": "1", "b": "2"}`,
	}, {
		name: "nested inside an any map",
		in:   map[string]any{"cmd": []string{"sh", "-c"}, "rc": 1},
		want: `{"cmd": ["sh", "-c"], "rc": 1}`,
	}, {
		name: "untyped slice still works",
		in:   []any{"a", 1, true},
		want: `["a", 1, true]`,
	}, {
		// []byte stays a base64 string: turning it into an array of
		// numbers would be a different VALUE, not a differently-spaced
		// one.
		name: "byte slice is still base64",
		in:   []byte("hi"),
		want: `"aGk="`,
	}, {
		name: "empty typed slice",
		in:   []string{},
		want: `[]`,
	}}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ToJSON(tt.in, 0)
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Errorf("ToJSON(%#v) = %s, want %s", tt.in, got, tt.want)
			}
		})
	}
}
