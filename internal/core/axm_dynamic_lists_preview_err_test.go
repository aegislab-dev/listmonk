package core

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/lib/pq"
)

func TestClassifyPreviewError(t *testing.T) {
	pg := func(code string) error { return fmt.Errorf("wrapped: %w", &pq.Error{Code: pq.ErrorCode(code)}) }
	for _, tc := range []struct {
		name string
		err  error
		want DynamicPreviewReason
	}{
		{"nil", nil, ""},
		// The 11/23 production error: could not resize shared memory segment ... No space left on device.
		{"shared memory full", pg("53100"), PreviewReasonResource},
		{"out of memory", pg("53200"), PreviewReasonResource},
		{"statement timeout", pg("57014"), PreviewReasonTimeout},
		{"context deadline", fmt.Errorf("query: %w", context.DeadlineExceeded), PreviewReasonTimeout},
		{"text to number", pg("22P02"), PreviewReasonNumber},
		{"number out of range", pg("22003"), PreviewReasonNumber},
		{"syntax error", pg("42601"), PreviewReasonOther},
		{"not a DB error", errors.New("boom"), PreviewReasonOther},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := classifyPreviewError(tc.err); got != tc.want {
				t.Fatalf("got %q want %q", got, tc.want)
			}
		})
	}
}
