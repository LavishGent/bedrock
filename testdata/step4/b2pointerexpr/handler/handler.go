package handler

import (
	"database/sql"
	"reflect"

	"example.com/s4/b2pointerexpr/helper"
)

// call is a pointer method expression: a wrapper of (reflect.Value).Call.
var call = (*reflect.Value).Call

// Run calls through the pointer wrapper.
func Run(db *sql.DB) {
	v := reflect.ValueOf(helper.Raw)
	call(&v, []reflect.Value{reflect.ValueOf(db)})
}
