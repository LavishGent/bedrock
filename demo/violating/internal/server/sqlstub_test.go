package server_test

// A test-only database/sql driver. It lives in a _test.go file, so it is not
// part of any analysis unit (test files are excluded) and cannot add or hide
// database.query facts. DSN "ok" serves fixed item rows; DSN "fail" errors.

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
)

func init() { sql.Register("demo-stub", stubDriver{}) }

type stubDriver struct{}

func (stubDriver) Open(dsn string) (driver.Conn, error) { return &stubConn{fail: dsn == "fail"}, nil }

type stubConn struct{ fail bool }

var errStub = errors.New("stub: query failed")

func (c *stubConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("stub: prepare unsupported")
}
func (c *stubConn) Close() error              { return nil }
func (c *stubConn) Begin() (driver.Tx, error) { return nil, errors.New("stub: tx unsupported") }

func (c *stubConn) QueryContext(_ context.Context, query string, _ []driver.NamedValue) (driver.Rows, error) {
	if c.fail {
		return nil, errStub
	}
	if query != "SELECT id, name, cents FROM items ORDER BY id" {
		return nil, errors.New("stub: unexpected query " + query)
	}
	return &stubRows{rows: [][]driver.Value{{int64(1), "widget", int64(250)}, {int64(2), "gadget", int64(1000)}}}, nil
}

type stubRows struct {
	rows [][]driver.Value
	i    int
}

func (r *stubRows) Columns() []string { return []string{"id", "name", "cents"} }
func (r *stubRows) Close() error      { return nil }
func (r *stubRows) Next(dest []driver.Value) error {
	if r.i >= len(r.rows) {
		return io.EOF
	}
	copy(dest, r.rows[r.i])
	r.i++
	return nil
}

func openStub(dsn string) *sql.DB {
	db, err := sql.Open("demo-stub", dsn)
	if err != nil {
		panic(err)
	}
	return db
}
