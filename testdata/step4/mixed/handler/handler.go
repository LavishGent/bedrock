package handler

import (
	"database/sql"
	"reflect"
)

// Serve has a real violation and a reflective call: the verdict is 1 and
// the reflection diagnostic is still listed as indeterminate.
func Serve(db *sql.DB, f any) {
	_, _ = db.Exec("SELECT 1") // FAIL
	reflect.ValueOf(f).Call(nil)
}
