package handler

import (
	"database/sql"
	"reflect"
)

// Serve calls a function through reflection: the callee is not modeled.
func Serve(db *sql.DB, f any) {
	reflect.ValueOf(f).Call([]reflect.Value{reflect.ValueOf(db)})
}
