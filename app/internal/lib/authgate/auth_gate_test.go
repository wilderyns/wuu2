package authgate

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestExtractCodeIgnoresQueryString(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/auth/applemusic/start?code=leaked", nil)
	if got := extractCode(req); got != "" {
		t.Fatalf("expected empty code from query string, got %q", got)
	}
}

func TestRateLimiterBlocksAfterLimit(t *testing.T) {
	limiter := newIPRateLimiter(2, time.Minute)
	now := time.Now()

	if !limiter.allow("127.0.0.1", now) {
		t.Fatal("expected first attempt to pass")
	}
	if !limiter.allow("127.0.0.1", now.Add(time.Second)) {
		t.Fatal("expected second attempt to pass")
	}
	if limiter.allow("127.0.0.1", now.Add(2*time.Second)) {
		t.Fatal("expected third attempt to be blocked")
	}
}
