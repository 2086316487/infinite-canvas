package service

import (
	"net/http"
	"testing"
	"time"
)

func TestImageTaskStatusRetryable(t *testing.T) {
	if !imageTaskStatusRetryable(http.StatusTooManyRequests) {
		t.Fatal("429 should be retryable")
	}
	if !imageTaskStatusRetryable(http.StatusBadGateway) {
		t.Fatal("502 should be retryable")
	}
	if imageTaskStatusRetryable(http.StatusBadRequest) {
		t.Fatal("400 should not be retryable")
	}
	if imageTaskStatusRetryable(http.StatusUnauthorized) {
		t.Fatal("401 should not be retryable")
	}
}

func TestImageTaskRetryDelay(t *testing.T) {
	if got, want := imageTaskRetryDelay(1), 5*time.Second; got != want {
		t.Fatalf("imageTaskRetryDelay(1) = %s, want %s", got, want)
	}
	if got, want := imageTaskRetryDelay(2), 15*time.Second; got != want {
		t.Fatalf("imageTaskRetryDelay(2) = %s, want %s", got, want)
	}
	if got, want := imageTaskRetryDelay(3), 25*time.Second; got != want {
		t.Fatalf("imageTaskRetryDelay(3) = %s, want %s", got, want)
	}
}
