package ext

import (
	"reflect"
	"unsafe"
)

// Do is outside the rule's scope but reached from a governed Entry: its
// reflective call is relevant (reachable_from_governed_entry).
func Do(f any) { reflect.ValueOf(f).Call(nil) }

// Unreached is outside the scope and no governed Entry reaches it: its
// unsafe conversion is a warning only.
func Unreached(p *int) uintptr { return uintptr(unsafe.Pointer(p)) }
