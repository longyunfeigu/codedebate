package provider

import (
	"errors"
	"testing"
)

func TestWithRetry_SucceedsFirstTry(t *testing.T) {
	callCount := 0
	result, err := WithRetry(func() (string, error) {
		callCount++
		return "success", nil
	}, &RetryOptions{
		MaxAttempts: 3,
		BackoffMs:   []int{1, 1, 1},
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "success" {
		t.Errorf("result = %q, want %q", result, "success")
	}
	if callCount != 1 {
		t.Errorf("callCount = %d, want 1", callCount)
	}
}

func TestWithRetry_SucceedsAfterRetry(t *testing.T) {
	callCount := 0
	result, err := WithRetry(func() (string, error) {
		callCount++
		if callCount < 3 {
			return "", errors.New("timeout")
		}
		return "recovered", nil
	}, &RetryOptions{
		MaxAttempts: 3,
		BackoffMs:   []int{1, 1, 1},
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "recovered" {
		t.Errorf("result = %q, want %q", result, "recovered")
	}
	if callCount != 3 {
		t.Errorf("callCount = %d, want 3", callCount)
	}
}

func TestWithRetry_ExhaustsRetries(t *testing.T) {
	callCount := 0
	_, err := WithRetry(func() (string, error) {
		callCount++
		return "", errors.New("timeout error")
	}, &RetryOptions{
		MaxAttempts: 3,
		BackoffMs:   []int{1, 1, 1},
	})

	if err == nil {
		t.Fatal("expected error after exhausting retries, got nil")
	}
	if callCount != 3 {
		t.Errorf("callCount = %d, want 3", callCount)
	}
}

func TestWithRetry_NonTransientError(t *testing.T) {
	callCount := 0
	_, err := WithRetry(func() (string, error) {
		callCount++
		return "", errors.New("permanent failure")
	}, &RetryOptions{
		MaxAttempts: 3,
		BackoffMs:   []int{1, 1, 1},
	})

	if err == nil {
		t.Fatal("expected error for non-transient failure, got nil")
	}
	if callCount != 1 {
		t.Errorf("callCount = %d, want 1 (should not retry non-transient errors)", callCount)
	}
}

func TestIsTransientError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil error", nil, false},
		{"timeout", errors.New("request timeout"), true},
		{"connection reset", errors.New("connection reset by peer"), true},
		{"econnreset", errors.New("read: econnreset"), true},
		{"rate limit", errors.New("rate limit exceeded"), true},
		{"429 status", errors.New("HTTP 429 too many requests"), true},
		{"503 status", errors.New("HTTP 503 service unavailable"), true},
		{"502 status", errors.New("HTTP 502 bad gateway"), true},
		{"permanent error", errors.New("invalid argument"), false},
		{"auth error", errors.New("unauthorized"), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isTransientError(tt.err)
			if got != tt.want {
				t.Errorf("isTransientError(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}
