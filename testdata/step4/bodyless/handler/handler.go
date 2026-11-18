package handler

import _ "unsafe" // for go:linkname

// nanotime has no Go body: it is linked to the runtime.
//
//go:linkname nanotime runtime.nanotime
func nanotime() int64

// Now calls the bodyless function.
func Now() int64 { return nanotime() }
