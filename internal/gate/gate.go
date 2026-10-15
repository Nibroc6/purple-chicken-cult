// Package gate is the visibility gate (ARCHITECTURE §13, plan §4.4): the ONLY
// layer that touches the database. This root package holds what handlers may see:
// view objects built from the viewer's horizon H(V), opaque per-viewer refs, the
// closed set of failures, and the request charge class.
//
// Contract file (SCAFFOLD): additive changes only, through the contract steward.
package gate

// Ref is an opaque per-viewer reference (platform/refs). It is the only way users,
// posts and other targets appear in the API.
type Ref string

// Class is a request charge class (plan §5.1): Kind 'r' read, 'w' write,
// 'a' anonymous (per client prefix), '-' free (reviewed allowlist).
type Class struct {
	Kind byte
	Name string
}

// Charge class kinds.
const (
	ClassRead  byte = 'r'
	ClassWrite byte = 'w'
	ClassAnon  byte = 'a'
	ClassFree  byte = '-'
)
