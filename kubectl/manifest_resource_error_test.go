package kubectl

import (
	"errors"
	"fmt"
	"testing"

	"github.com/cenkalti/backoff/v4"
)

func TestErrorConditionError_implements_error(t *testing.T) {
	var err error = &errorConditionError{Msg: "test error"}
	if err.Error() != "test error" {
		t.Fatalf("expected 'test error', got %q", err.Error())
	}
}

func TestErrorConditionError_detectable_via_errorsAs(t *testing.T) {
	original := &errorConditionError{Msg: "field matched"}
	wrapped := fmt.Errorf("failed to wait: %w", original)

	var ece *errorConditionError
	if !errors.As(wrapped, &ece) {
		t.Fatal("errors.As should detect errorConditionError through wrapping")
	}
	if ece.Msg != "field matched" {
		t.Fatalf("unexpected message: %q", ece.Msg)
	}
}

func TestErrorConditionError_not_detected_for_other_errors(t *testing.T) {
	other := fmt.Errorf("some other error")
	var ece *errorConditionError
	if errors.As(other, &ece) {
		t.Fatal("errors.As should NOT detect errorConditionError for unrelated errors")
	}
}

func TestErrorConditionError_survives_backoff_permanent(t *testing.T) {
	original := &errorConditionError{Msg: "condition met"}
	permanent := backoff.Permanent(original)

	// After backoff.Retry returns a Permanent error, the inner error is unwrapped.
	// Verify we can still detect the errorConditionError.
	var ece *errorConditionError
	if !errors.As(permanent, &ece) {
		t.Fatal("errors.As should detect errorConditionError through backoff.Permanent wrapping")
	}
}

func TestErrorConditionError_double_wrapped(t *testing.T) {
	original := &errorConditionError{Msg: "crash loop"}
	wrapped := fmt.Errorf("failed to wait for conditions: %w",
		fmt.Errorf("wait error: %w", original))

	var ece *errorConditionError
	if !errors.As(wrapped, &ece) {
		t.Fatal("errors.As should detect errorConditionError through multiple layers of wrapping")
	}
	if ece.Msg != "crash loop" {
		t.Fatalf("unexpected message: %q", ece.Msg)
	}
}
