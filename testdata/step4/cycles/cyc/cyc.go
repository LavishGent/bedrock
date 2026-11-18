package cyc

import "database/sql"

// A and B form a cycle entered only from repo.Run: it passes.
func A(db *sql.DB, n int) {
	if n > 0 {
		B(db, n-1)
	}
	_, _ = db.Exec("SELECT 1")
}

// B closes the A/B cycle.
func B(db *sql.DB, n int) { A(db, n) }

// X and Y form a cycle that no Entry reaches (each has an incoming edge), so
// the boundary guards nothing and the call fails.
func X(db *sql.DB, n int) {
	if n > 0 {
		Y(db, n-1)
	}
	_, _ = db.Exec("SELECT 2") // FAIL
}

// Y closes the X/Y cycle.
func Y(db *sql.DB, n int) { X(db, n) }

// Call makes a dynamic call with no possible target in the universe: it is
// exempt (no_possible_target), not an unknown call.
func Call(f func()) { f() }
