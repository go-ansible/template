package template

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// The lookup plugins in this file are the PURE ones: they transform the
// terms they are given and touch nothing outside the process. They are
// also exactly the set `with_<name>` needs, which is why they come
// together — real Ansible implements with_items, with_dict and the rest
// as these same plugins with wantlist forced on.
//
// Every expected value in lookups_data_test.go was produced by RUNNING
// real ansible-core 2.21.4, not by reading its plugins.

func registerDataLookups(lookups map[string]lookupFunc) {
	lookups["items"] = lookupItems
	lookups["list"] = lookupList
	lookups["flattened"] = lookupFlattened
	lookups["dict"] = lookupDict
	lookups["nested"] = lookupNested
	lookups["together"] = lookupTogether
	lookups["indexed_items"] = lookupIndexedItems
	lookups["sequence"] = lookupSequence
	lookups["subelements"] = lookupSubelements
}

// lookupItems flattens ONE level: query('items', [1,2], 3) is
// [1, 2, 3]. Contrast lookupList, which flattens none.
func lookupItems(terms []any, _ map[string]any, _ map[string]any) ([]any, error) {
	out := []any{}
	for _, t := range terms {
		if list, ok := asAnyList(t); ok {
			out = append(out, list...)
			continue
		}
		out = append(out, t)
	}
	return out, nil
}

// lookupList returns its terms untouched: query('list', [1,2], 3) is
// [[1, 2], 3]. Measured, because "list" reads like it would flatten.
func lookupList(terms []any, _ map[string]any, _ map[string]any) ([]any, error) {
	return append([]any{}, terms...), nil
}

// lookupFlattened flattens to ANY depth: [1,[2,[3]]], 4 is 1,2,3,4.
func lookupFlattened(terms []any, _ map[string]any, _ map[string]any) ([]any, error) {
	out := []any{}
	var walk func(v any)
	walk = func(v any) {
		if list, ok := asAnyList(v); ok {
			for _, item := range list {
				walk(item)
			}
			return
		}
		out = append(out, v)
	}
	for _, t := range terms {
		walk(t)
	}
	return out, nil
}

// lookupDict turns a mapping into {key, value} pairs.
//
// ONE DISCLOSED DIVERGENCE: real iterates the dict in DOCUMENT order
// (a Python dict preserves insertion order, so {zulu, alpha, mike}
// comes back in that order). A Go map has no order at all, and this
// port decodes YAML mappings into map[string]any, so the order is
// unrecoverable by the time a lookup sees it. Keys are sorted instead
// — deterministic, and identical to real whenever the document was
// already in key order. Restoring real's order would take an ordered
// map through the whole variable pipeline.
func lookupDict(terms []any, _ map[string]any, _ map[string]any) ([]any, error) {
	out := []any{}
	for _, t := range terms {
		m, ok := t.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("with_dict expects a dict, got %T", t)
		}
		keys := make([]string, 0, len(m))
		for k := range m {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			out = append(out, map[string]any{"key": k, "value": m[k]})
		}
	}
	return out, nil
}

// lookupNested is the cartesian product, FIRST term varying slowest:
// ([1,2], ['a','b']) gives [1,a] [1,b] [2,a] [2,b].
func lookupNested(terms []any, _ map[string]any, _ map[string]any) ([]any, error) {
	out := []any{[]any{}}
	for _, t := range terms {
		list, ok := asAnyList(t)
		if !ok {
			list = []any{t}
		}
		next := []any{}
		for _, prefix := range out {
			for _, item := range list {
				row := append([]any{}, prefix.([]any)...)
				next = append(next, append(row, item))
			}
		}
		out = next
	}
	return out, nil
}

// lookupTogether zips its terms, padding the short ones with null —
// Python's itertools.zip_longest, not zip: ([1,2,3], ['a','b']) gives
// a third row [3, null] rather than stopping at two.
func lookupTogether(terms []any, _ map[string]any, _ map[string]any) ([]any, error) {
	lists := make([][]any, 0, len(terms))
	longest := 0
	for _, t := range terms {
		list, ok := asAnyList(t)
		if !ok {
			list = []any{t}
		}
		lists = append(lists, list)
		if len(list) > longest {
			longest = len(list)
		}
	}
	out := []any{}
	for i := 0; i < longest; i++ {
		row := make([]any, len(lists))
		for j, list := range lists {
			if i < len(list) {
				row[j] = list[i]
			}
		}
		out = append(out, row)
	}
	return out, nil
}

// lookupIndexedItems pairs each item with its index: ['x','y'] gives
// [0,'x'] [1,'y'].
func lookupIndexedItems(terms []any, _ map[string]any, _ map[string]any) ([]any, error) {
	flat, err := lookupItems(terms, nil, nil)
	if err != nil {
		return nil, err
	}
	out := make([]any, 0, len(flat))
	for i, item := range flat {
		out = append(out, []any{i, item})
	}
	return out, nil
}

// lookupSequence generates a number range as STRINGS — measured:
// query('sequence', 'start=1 end=4') is ["1","2","3","4"], not
// integers. format= applies a printf verb per item.
func lookupSequence(terms []any, _ map[string]any, _ map[string]any) ([]any, error) {
	out := []any{}
	for _, t := range terms {
		spec, ok := t.(string)
		if !ok {
			return nil, fmt.Errorf("with_sequence expects a string spec, got %T", t)
		}
		start, end, stride := 1, 0, 1
		format := "%d"
		haveEnd := false
		for _, field := range strings.Fields(spec) {
			key, value, found := strings.Cut(field, "=")
			if !found {
				return nil, fmt.Errorf("with_sequence: %q is not key=value", field)
			}
			switch key {
			case "format":
				format = value
				continue
			case "start", "end", "stride", "count":
			default:
				return nil, fmt.Errorf("with_sequence: unknown field %q", key)
			}
			n, err := strconv.Atoi(value)
			if err != nil {
				return nil, fmt.Errorf("with_sequence: %s=%q is not a number", key, value)
			}
			switch key {
			case "start":
				start = n
			case "end":
				end, haveEnd = n, true
			case "stride":
				stride = n
			case "count":
				end, haveEnd = start+n-1, true
			}
		}
		if !haveEnd {
			return nil, fmt.Errorf("with_sequence: needs an end= or count=")
		}
		if stride == 0 {
			return nil, fmt.Errorf("with_sequence: stride cannot be zero")
		}
		for i := start; (stride > 0 && i <= end) || (stride < 0 && i >= end); i += stride {
			out = append(out, fmt.Sprintf(format, i))
		}
	}
	return out, nil
}

// lookupSubelements pairs each parent with each element of one of its
// keys. The parent comes back WITHOUT that key — measured: the subkey
// is removed from the copy, so a template sees only the rest.
//
// skip_missing (a kwarg, or a trailing options dict, both of which
// real accepts) turns a missing or null subkey into no rows instead of
// an error.
func lookupSubelements(terms []any, _ map[string]any, kwargs map[string]any) ([]any, error) {
	if len(terms) < 2 {
		return nil, fmt.Errorf("with_subelements needs a list and a subkey")
	}
	// Real takes its options either as kwargs or as a THIRD term.
	skipMissing, _ := kwargs["skip_missing"].(bool)
	if len(terms) >= 3 {
		if opts, ok := terms[2].(map[string]any); ok {
			if v, ok := opts["skip_missing"].(bool); ok {
				skipMissing = v
			}
		}
	}
	parents, ok := asAnyList(terms[0])
	if !ok {
		return nil, fmt.Errorf("with_subelements expects a list, got %T", terms[0])
	}
	subkey, ok := terms[1].(string)
	if !ok {
		return nil, fmt.Errorf("with_subelements expects a subkey name, got %T", terms[1])
	}

	out := []any{}
	for _, p := range parents {
		parent, ok := p.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("with_subelements expects dicts, got %T", p)
		}
		raw, present := parent[subkey]
		if !present || raw == nil {
			if skipMissing {
				continue
			}
			return nil, fmt.Errorf("with_subelements: %q is not a key in %v", subkey, parent)
		}
		children, ok := asAnyList(raw)
		if !ok {
			return nil, fmt.Errorf("with_subelements: %q is not a list", subkey)
		}
		stripped := make(map[string]any, len(parent)-1)
		for k, v := range parent {
			if k != subkey {
				stripped[k] = v
			}
		}
		for _, child := range children {
			out = append(out, []any{stripped, child})
		}
	}
	return out, nil
}

// asAnyList reports whether v is a list, and returns it as []any. Typed
// slices decoded from elsewhere are not converted here on purpose:
// everything reaching a lookup has already been through the YAML/Jinja
// pipeline, which produces []any.
func asAnyList(v any) ([]any, bool) {
	list, ok := v.([]any)
	return list, ok
}
