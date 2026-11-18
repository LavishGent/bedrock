package handler

import (
	"database/sql"
	"reflect"

	"example.com/s4/b2field/helper"
)

// box stores a reflective call in a field.
type box struct {
	f func([]reflect.Value) []reflect.Value
}

// Run calls through the stored field.
func Run(db *sql.DB) {
	b := box{f: reflect.ValueOf(helper.Raw).Call}
	b.f([]reflect.Value{reflect.ValueOf(db)})
}
