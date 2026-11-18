// Package a is the root of the subtree that owns a/internal/secret. Nothing
// imports it; the fixture policy's forbid_import target needs a unit that
// exists (empty-match rule) without creating a violation that would mask the
// load failure's INDETERMINATE.
package a

// Name identifies the package.
const Name = "a"
