// Package lined imports the forbidden package after a //line directive that
// points outside the repository. The violation must still be reported, at the
// physical location of the import.
package lined

//line /outside/the/repo/generated.go:1
import "example.com/regress/forbidden"

// Y re-exports the forbidden constant.
const Y = forbidden.X
