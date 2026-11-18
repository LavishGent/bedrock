package handler

import (
	"database/sql"
	"reflect"

	"example.com/s4/b2interface/helper"
)

// Run calls reflect.Value.Call through an interface.
func Run(db *sql.DB) {
	var c interface {
		Call([]reflect.Value) []reflect.Value
	} = reflect.ValueOf(helper.Raw)
	c.Call([]reflect.Value{reflect.ValueOf(db)})
}
