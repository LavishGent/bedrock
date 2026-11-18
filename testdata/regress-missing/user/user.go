// Package user imports an in-module package that does not exist. The missing
// package is governed (inside the module path) and has a load error and no
// compiled files, so the governing rule must be INDETERMINATE.
package user

import _ "example.com/regressmissing/absent"
