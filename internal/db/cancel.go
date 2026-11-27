package db

import (
	"context"
	"fmt"
	"time"
)

// Stopping a running statement.
//
// Cancelling the context alone only makes the Go driver drop its connection: MySQL keeps executing
// the statement (a long DELETE runs to the end) and an open transaction on that connection is lost.
// So statements run on a context that is not cancelled directly; when the caller's context is
// cancelled the statement is stopped on the server (MySQL KILL QUERY, PostgreSQL pg_cancel_backend)
// from a second connection. The statement then fails with "query interrupted"/"canceling statement",
// the connection and its transaction stay usable. If the server has not stopped it after
// killGrace the connection is dropped after all.

const killGrace = 5 * time.Second

// backendID is the server-side id of the connection ex runs on: the MySQL thread id or the
// PostgreSQL backend pid. 0 when it cannot be determined (then cancelling falls back to the driver).
func (c *Conn) backendID(ctx context.Context, ex execer) int64 {
	q := "SELECT CONNECTION_ID()"
	if c.isPG() {
		q = "SELECT pg_backend_pid()"
	}
	rows, err := ex.QueryContext(ctx, q)
	if err != nil {
		return 0
	}
	defer rows.Close()
	var id int64
	if rows.Next() && rows.Scan(&id) == nil {
		return id
	}
	return 0
}

// killQuery stops whatever statement backend id is running, from another pooled connection.
func (c *Conn) killQuery(id int64) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if c.isPG() {
		_, err := c.DB.ExecContext(ctx, "SELECT pg_cancel_backend($1)", id)
		return err
	}
	_, err := c.DB.ExecContext(ctx, fmt.Sprintf("KILL QUERY %d", id))
	return err
}

// interruptible runs fn on a context that survives cancellation of ctx; cancelling ctx kills
// the statement of backend id on the server instead. With id 0 fn simply gets ctx.
func (c *Conn) interruptible(ctx context.Context, id int64, fn func(context.Context) error) error {
	if id == 0 {
		return fn(ctx)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	qctx, qcancel := context.WithCancel(context.WithoutCancel(ctx))
	defer qcancel()
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-done:
			return
		case <-ctx.Done():
		}
		if err := c.killQuery(id); err != nil {
			qcancel() // could not reach the server: drop the connection as before
			return
		}
		select {
		case <-done:
		case <-time.After(killGrace):
			qcancel()
		}
	}()
	err := fn(qctx)
	if err != nil && ctx.Err() != nil {
		return fmt.Errorf("cancelled: %w", ctx.Err())
	}
	return err
}
