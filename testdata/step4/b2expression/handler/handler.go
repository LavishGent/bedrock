package handler

import (
	"database/sql"
	"reflect"

	"example.com/s4/b2expression/helper"
)

// call is a method expression stored in a function value.
var call = (reflect.Value).Call

// Run calls through the method expression.
func Run(db *sql.DB) {
	call(reflect.ValueOf(helper.Raw), []reflect.Value{reflect.ValueOf(db)})
}
