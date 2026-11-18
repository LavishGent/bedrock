package handler

import (
	"database/sql"
	"reflect"

	"example.com/s4/b2direct/helper"
)

// Run calls helper.Raw by direct reflection (review B2 control).
func Run(db *sql.DB) {
	reflect.ValueOf(helper.Raw).Call([]reflect.Value{reflect.ValueOf(db)})
}
