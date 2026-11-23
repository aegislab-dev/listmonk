package core

import (
	"context"
	"errors"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/lib/pq"
)

// dynamicPreviewTimeout bounds one preview query. A preview that runs longer than this
// (or than the DB's own statement_timeout) answers with a "took too long" message instead of
// leaving the screen waiting.
const dynamicPreviewTimeout = 60 * time.Second

// DynamicPreviewReason is why a count check failed, in terms an operator can act on.
type DynamicPreviewReason string

const (
	// PreviewReasonResource is the DB running out of a server resource (SQLSTATE class 53),
	// for example the shared memory a parallel query needs ("No space left on device").
	PreviewReasonResource DynamicPreviewReason = "resource"
	// PreviewReasonTimeout is a query that did not finish within dynamicPreviewTimeout (or the
	// DB's own statement_timeout).
	PreviewReasonTimeout DynamicPreviewReason = "timeout"
	// PreviewReasonNumber is a value the DB could not read as a number.
	PreviewReasonNumber DynamicPreviewReason = "number"
	// PreviewReasonOther is anything else.
	PreviewReasonOther DynamicPreviewReason = "other"
)

// DynamicPreviewError wraps the DB error of a failed preview with its classified reason.
type DynamicPreviewError struct {
	Reason DynamicPreviewReason
	Err    error
}

func (e *DynamicPreviewError) Error() string { return string(e.Reason) + ": " + e.Err.Error() }
func (e *DynamicPreviewError) Unwrap() error { return e.Err }

// classifyPreviewError maps a DB error to a reason. nil is nil-safe: it returns "".
func classifyPreviewError(err error) DynamicPreviewReason {
	if err == nil {
		return ""
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return PreviewReasonTimeout
	}
	var pe *pq.Error
	if errors.As(err, &pe) {
		code := string(pe.Code)
		switch {
		// query_canceled: statement_timeout or a cancelled context.
		case code == "57014":
			return PreviewReasonTimeout
		// insufficient_resources: disk_full (53100, also raised for a full /dev/shm),
		// out_of_memory, too_many_connections, configuration_limit_exceeded.
		case len(code) == 5 && code[:2] == "53":
			return PreviewReasonResource
		// invalid_text_representation, numeric_value_out_of_range, division_by_zero.
		case code == "22P02" || code == "22003" || code == "22012":
			return PreviewReasonNumber
		}
	}
	return PreviewReasonOther
}

// runPreviewJob runs one preview query. When the DB reports it ran out of a resource, it
// runs the query once more with parallel workers off: a parallel query needs a shared memory
// segment (/dev/shm) that a small container can fill, a serial one does not.
func (c *Core) runPreviewJob(job func(ctx context.Context, use func(*sqlx.Stmt) *sqlx.Stmt) error) error {
	ctx, cancel := context.WithTimeout(context.Background(), dynamicPreviewTimeout)
	defer cancel()
	err := job(ctx, func(s *sqlx.Stmt) *sqlx.Stmt { return s })
	if classifyPreviewError(err) != PreviewReasonResource {
		return err
	}
	c.log.Printf("dynamic list preview: %v; retrying without parallel workers", err)
	tx, txErr := c.db.BeginTxx(ctx, nil)
	if txErr != nil {
		return err
	}
	defer tx.Rollback()
	// The statements are prepared, and a cached (generic) plan keeps its parallel nodes whatever
	// max_parallel_workers_per_gather says now, so the retry also asks for a fresh plan.
	if _, txErr := tx.ExecContext(ctx, "SET LOCAL max_parallel_workers_per_gather = 0; SET LOCAL plan_cache_mode = force_custom_plan"); txErr != nil {
		return err
	}
	return job(ctx, func(s *sqlx.Stmt) *sqlx.Stmt { return tx.Stmtx(s) })
}
