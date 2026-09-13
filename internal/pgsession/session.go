// Package pgsession shares one explicitly owned SQL session across nested locks
// and journal commits. It avoids reserving another pool slot while holding a lock.
package pgsession

import (
	"context"
	"database/sql"
	"errors"
)

type key struct{}
type session struct {
	db   *sql.DB
	conn *sql.Conn
}
type Executor interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
	BeginTx(context.Context, *sql.TxOptions) (*sql.Tx, error)
}

func With(ctx context.Context, db *sql.DB, work func(context.Context, *sql.Conn) error) error {
	if db == nil || work == nil {
		return errors.New("SQL session requires database and work")
	}
	if existing, ok := ctx.Value(key{}).(session); ok {
		if existing.db != db {
			return errors.New("nested SQL session belongs to another database")
		}
		return work(ctx, existing.conn)
	}
	conn, err := db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	return work(context.WithValue(ctx, key{}, session{db, conn}), conn)
}
func Use(ctx context.Context, db *sql.DB) Executor {
	if existing, ok := ctx.Value(key{}).(session); ok && existing.db == db {
		return existing.conn
	}
	return db
}
