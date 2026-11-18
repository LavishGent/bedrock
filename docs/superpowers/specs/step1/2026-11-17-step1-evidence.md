# Step 1 evidence: repository scaffold, policy contract, demo

**Date:** 2026-11-17
**Plan:** `../2026-11-17-bedrock-poc-plan.md`, section 8, step 1

## Deliverables

| Item | Path |
| --- | --- |
| Policy, all three rule kinds | `.bedrock/policy.toml` |
| Policy contract v1 (S1), with the decision A assumption | `../policy/README.md` |
| Router support matrix v1 (B4) | `../routers/README.md` |
| Demo, clean state | `demo/clean` (module `example.com/demo`) |
| Demo, violating state | `demo/violating` (same module path, so the one policy governs both) |
| Broken fixture | `testdata/broken` (module `example.com/broken`) |
| Ownership | `.github/CODEOWNERS` |
| Schema bump to 0.2 | `../schema/` (report `assumptions`, required by decision A) |

## Build, vet and test

Run with go1.26.11:

```
$ cd demo/clean && go build ./... && go vet ./... && go test -count=1 ./...
?   	example.com/demo/cmd/demo	[no test files]
ok  	example.com/demo/internal/auth	0.350s
?   	example.com/demo/internal/auth/private	[no test files]
?   	example.com/demo/internal/billing	[no test files]
?   	example.com/demo/internal/handler	[no test files]
?   	example.com/demo/internal/repo	[no test files]
ok  	example.com/demo/internal/server	0.232s
$ cd demo/violating && go build ./... && go vet ./... && go test -count=1 ./...
?   	example.com/demo/cmd/demo	[no test files]
ok  	example.com/demo/internal/auth	0.304s
?   	example.com/demo/internal/auth/private	[no test files]
?   	example.com/demo/internal/billing	[no test files]
?   	example.com/demo/internal/handler	[no test files]
?   	example.com/demo/internal/repo	[no test files]
ok  	example.com/demo/internal/server	0.188s
```

## The auth tests catch a no-op `RequireAuth`

The test was run on a scratch copy of `demo/clean` with `RequireAuth` replaced by `func RequireAuth(next http.Handler) http.Handler { return next }`. The tree itself is unchanged.

```
--- FAIL: TestRequireAuthRejects (0.00s)
    --- FAIL: TestRequireAuthRejects/missing (0.00s)
        auth_test.go:21: handler reached without an authenticated user in context
        auth_test.go:43: status = 200, want 401
    --- FAIL: TestRequireAuthRejects/malformed (0.00s)
        auth_test.go:21: handler reached without an authenticated user in context
        auth_test.go:43: status = 200, want 401
    --- FAIL: TestRequireAuthRejects/unknown (0.00s)
        auth_test.go:21: handler reached without an authenticated user in context
        auth_test.go:43: status = 200, want 401
--- FAIL: TestRequireAuthAccepts (0.00s)
    auth_test.go:21: handler reached without an authenticated user in context
FAIL
FAIL	example.com/demo/internal/auth	1.015s
--- FAIL: TestRoutes (0.00s)
    --- FAIL: TestRoutes/items_needs_auth (0.00s)
        server_test.go:59: GET /api/v1/items: status 200, want 401
    --- FAIL: TestRoutes/create_needs_auth (0.00s)
        server_test.go:59: POST /api/v1/items: status 201, want 401
    --- FAIL: TestRoutes/billing_with_auth (0.00s)
        server_test.go:59: GET /api/v1/billing/summary: status 401, want 200
    --- FAIL: TestRoutes/export_needs_auth (0.00s)
        server_test.go:59: GET /internal/export: status 200, want 401
FAIL
FAIL	example.com/demo/internal/server	1.168s
```

The auth tests sit under `/demo/*/internal/auth/`, which CODEOWNERS protects together with the middleware.

## Broken fixture fails to load as intended

```
$ cd testdata/broken && go build ./...
package example.com/broken/b
	b/b.go:6:8: use of internal package example.com/broken/a/internal/secret not allowed
go build exit=1
```

## Seeded violations (exactly one per rule kind)

`diff -r demo/clean demo/violating` has no other semantic changes. The rest of the diff is plumbing: `handler.New` takes a `*sql.DB`, and one server test case is dropped because `Export` now needs a real DB.

| Rule | Kind | Location | What |
| --- | --- | --- | --- |
| `auth-private-import` | `forbid_import` | `demo/violating/internal/billing/billing.go:9` | `billing` imports `example.com/demo/internal/auth/private` (calls `private.IsAdmin`) |
| `routes-require-auth` | `require_middleware` | `demo/violating/internal/server/server.go:18` | `GET /api/v1/admin/users` registered on the root router, outside `r.Route("/api/v1", ...)` with `r.Use(auth.RequireAuth)` |
| `db-in-repo` | `restrict_concept_to_boundary` | `demo/violating/internal/handler/handler.go:95` | `(*Handlers).Export` calls `(*database/sql.DB).QueryContext` directly. It is reachable from the route handler `Entry` without passing through `internal/repo`. |

**Expected verdicts:**

- **`demo/clean`: `PASS` (0).** Under decision A, the clean module has no reflection, `unsafe` or bodyless calls. chi's only diagnostics in the step 0 spike were six zero-target sites, and those are exempt under `no_possible_target`.
- **`demo/violating`: `VIOLATION` (1)**, with three violations.

Both are to be confirmed by the analyzer in steps 2 to 4.
