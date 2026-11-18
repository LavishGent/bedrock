// Package b illegally imports another subtree's internal package. Go rejects
// it at load time ("use of internal package ... not allowed"), which must make
// Bedrock report INDETERMINATE (exit 2) through a package_load_error.
package b

import "example.com/broken/a/internal/secret"

// Key re-exports the secret.
var Key = secret.Key
