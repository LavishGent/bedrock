package handler

import (
	"database/sql"
	"reflect"

	"example.com/s4/b2generic/helper"
)

// invoke calls Call through a type parameter.
func invoke[T interface {
	Call([]reflect.Value) []reflect.Value
}](t T, args []reflect.Value) {
	t.Call(args)
}

// Run instantiates invoke with reflect.Value.
func Run(db *sql.DB) {
	invoke(reflect.ValueOf(helper.Raw), []reflect.Value{reflect.ValueOf(db)})
}
