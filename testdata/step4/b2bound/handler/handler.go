package handler

import (
	"database/sql"
	"reflect"

	"example.com/s4/b2bound/helper"
)

// Run calls through a bound method value of reflect.Value.Call.
func Run(db *sql.DB) {
	call := reflect.ValueOf(helper.Raw).Call
	call([]reflect.Value{reflect.ValueOf(db)})
}
