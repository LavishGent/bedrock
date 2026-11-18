package handler

import (
	"database/sql"
	"reflect"

	"example.com/s4/b2argument/helper"
)

// apply calls whatever it is given.
func apply(f func([]reflect.Value) []reflect.Value, args []reflect.Value) { f(args) }

// Run passes the reflective Call as an argument.
func Run(db *sql.DB) {
	apply(reflect.ValueOf(helper.Raw).Call, []reflect.Value{reflect.ValueOf(db)})
}
