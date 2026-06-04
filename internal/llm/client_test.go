package llm

import (
	"context"
	"testing"
)

func TestAPIErrorIsRetryable(t *testing.T) {
	tests := []struct {
		status    int
		retryable bool
	}{
		{429, true},
		{529, true},
		{503, true},
		{400, false},
		{401, false},
		{404, false},
	}
	for _, tt := range tests {
		err := &APIError{Status: tt.status, Type: "test", Message: "test"}
		if err.IsRetryable() != tt.retryable {
			t.Errorf("status %d: expected retryable=%v", tt.status, tt.retryable)
		}
	}
}

func TestRetryWithBackoff(t *testing.T) {
	ctx := context.Background()
	attempts := 0

	fn := func() (string, error) {
		attempts++
		if attempts < 3 {
			return "", &APIError{Status: 429, Type: "rate_limit", Message: "try later"}
		}
		return "success", nil
	}

	result, err := RetryWithBackoff(ctx, 3, fn, func(err error) bool {
		if apiErr, ok := err.(*APIError); ok {
			return apiErr.IsRetryable()
		}
		return false
	})
	if err != nil {
		t.Fatal(err)
	}
	if result != "success" {
		t.Errorf("got %q, want 'success'", result)
	}
	if attempts != 3 {
		t.Errorf("expected 3 attempts, got %d", attempts)
	}
}
