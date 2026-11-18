package handler

import "unsafe"

// Peek converts through unsafe.Pointer: memory reached through it is not
// modeled.
func Peek(p *int) uintptr { return uintptr(unsafe.Pointer(p)) }
