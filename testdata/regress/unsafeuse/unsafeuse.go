// Package unsafeuse imports unsafe directly. The fixture policy forbids it, so
// this must be a violation even though unsafe has no compiled files.
package unsafeuse

import "unsafe"

// Size reports the size of an int.
func Size() uintptr { return unsafe.Sizeof(0) }
