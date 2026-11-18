# Broken fixture (load failure)

This is its own module (`example.com/broken`), so ordinary loads of `demo/` never see it (plan section 10). `b` imports `a/internal/secret`, which Go's `internal` rule forbids. Loading it must produce a `package_load_error` diagnostic, and the result must be `INDETERMINATE` (exit 2).

Expected, with go1.26.11 and the pinned profile, run from this directory:

```
$ go build ./...
package example.com/broken/b
	b/b.go:6:8: use of internal package example.com/broken/a/internal/secret not allowed
$ echo $?
1
```
