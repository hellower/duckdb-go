package duckdb

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/duckdb/duckdb-go/v2/mapping"
	"github.com/stretchr/testify/require"
)

// TestPrepareContextInterruptsActiveBind holds the native prepare boundary until
// its connection receives an interrupt. A separate watchdog bounds a broken path.
func TestPrepareContextInterruptsActiveBind(t *testing.T) {
	type args struct {
		preCanceled bool
		query       bool
	}
	type want struct {
		interrupt bool
	}
	tests := []struct {
		name    string
		args    args
		want    want
		wantErr bool
	}{
		{name: "active cancellation interrupts bind", want: want{interrupt: true}},
		{name: "query path control", args: args{query: true}, want: want{interrupt: true}},
		{name: "pre-canceled context skips bind", args: args{preCanceled: true}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := openDbWrapper(t, "")
			defer closeDbWrapper(t, db)
			conn := openConnWrapper(t, db, context.Background())
			defer closeConnWrapper(t, conn)

			entered := make(chan struct{})
			interrupted := make(chan struct{})
			watchdog, stopWatchdog := context.WithTimeout(context.Background(), 2*time.Second)
			defer stopWatchdog()
			originalPrepare := mapping.PrepareExtractedStatement
			originalInterrupt := mapping.Interrupt
			var once sync.Once
			mapping.PrepareExtractedStatement = func(c mapping.Connection, s mapping.ExtractedStatements, i mapping.IdxT, out *mapping.PreparedStatement) mapping.State {
				close(entered)
				select {
				case <-interrupted:
				case <-watchdog.Done():
				}
				return originalPrepare(c, s, i, out)
			}
			mapping.Interrupt = func(mapping.Connection) { once.Do(func() { close(interrupted) }) }
			restore := func() {
				mapping.PrepareExtractedStatement = originalPrepare
				mapping.Interrupt = originalInterrupt
			}
			t.Cleanup(restore)

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if tt.args.preCanceled {
				cancel()
			}
			done := make(chan error, 1)
			go func() {
				done <- conn.Raw(func(raw any) error {
					if tt.args.query {
						result, err := raw.(*Conn).QueryContext(ctx, "SELECT 1", nil)
						if r, ok := result.(*rows); ok && r != nil {
							_ = r.Close()
						}
						return err
					}
					stmt, err := raw.(*Conn).PrepareContext(ctx, "SELECT 1")
					if prepared, ok := stmt.(*Stmt); ok && prepared != nil {
						_ = prepared.Close()
					}
					return err
				})
			}()
			if !tt.args.preCanceled {
				select {
				case <-entered:
				case <-watchdog.Done():
					t.Fatal("prepare did not enter the bind boundary")
				}
				cancel()
			}
			watchdogExpired := false
			select {
			case err := <-done:
				if tt.wantErr {
					require.ErrorIs(t, err, context.Canceled)
				}
			case <-watchdog.Done():
				watchdogExpired = true
				<-done
			}
			if watchdogExpired {
				t.Fatal("prepare did not settle before the watchdog")
			}
			if tt.want.interrupt {
				select {
				case <-interrupted:
				default:
					t.Fatal("PrepareContext did not interrupt the active bind")
				}
			} else {
				select {
				case <-entered:
					t.Fatal("pre-canceled prepare reached the bind boundary")
				default:
				}
				if !errors.Is(ctx.Err(), context.Canceled) {
					t.Fatal("pre-canceled context lost its cause")
				}
			}

			restore()
			var one int
			require.NoError(t, conn.QueryRowContext(context.Background(), "SELECT 1").Scan(&one))
			require.Equal(t, 1, one)
		})
	}
}
