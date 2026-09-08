package template

// omitType is Ansible's own omit sentinel type. A single zero-size value
// of this type is the only instance that ever exists (Omit, below), so
// equality comparison is exactly identity — matching real ansible-core's
// own Omit singleton (ansible._internal._templating._utils._OmitType),
// confirmed from source: "A placeholder singleton used to dynamically
// omit items from a dict/list/tuple/set when the value is Omit ... Omit
// values remaining in template results will be automatically dropped
// during template finalization."
type omitType struct{}

// Omit is the sentinel value the "omit" global evaluates to. A task
// argument written as "{{ x | default(omit) }}" renders to this value
// when x is undefined, and RenderValue drops the containing map key or
// list item rather than passing Omit through to a module.
var Omit omitType

// IsOmit reports whether v is the omit sentinel.
func IsOmit(v any) bool {
	_, ok := v.(omitType)
	return ok
}
