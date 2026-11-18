// Package underscore explicitly imports a package in a _-prefixed directory.
// ./... skips such directories, but an explicit import makes the loader
// compile it, so it is an analyzed unit, not an excluded file (review N2).
package underscore

import "example.com/underscore/_helper"

// Name re-exports the helper's name.
const Name = helper.Name
