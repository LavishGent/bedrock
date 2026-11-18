package handler

import (
	"database/sql"
	"reflect"

	"example.com/s4/b2callslice/helper"
)

// Run calls through a bound method value of CallSlice.
func Run(db *sql.DB) {
	cs := reflect.ValueOf(helper.Raw).CallSlice
	cs([]reflect.Value{reflect.ValueOf(db)})
}
