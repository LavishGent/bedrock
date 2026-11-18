package repo

import (
	"database/sql"

	"example.com/s4/cexpass/mid"
)

// B is the boundary symbol. The extra edge b->u removes U from Entry
// (plan decision 4 detail: indegree Entry is not monotone).
func B(db *sql.DB) {
	mid.C(db)
	mid.U(db)
}
