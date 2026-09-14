package insights

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

// A limit that never clears ends in ErrLimited after the set number of
// attempts, so a batch can stop instead of spending its quota on refusals.
func TestGeminiStopsWithErrLimitedWhenTheLimitOutlastsEveryAttempt(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":{"message":"quota","details":[{"@type":"type.googleapis.com/google.rpc.RetryInfo","retryDelay":"0.01s"}]}}`))
	}))
	defer srv.Close()

	_, err := Gemini{APIKey: "k", Model: "gemini-test", BaseURL: srv.URL}.Generate(context.Background(), "sys", "user", schema)
	if !errors.Is(err, ErrLimited) {
		t.Fatalf("error %v, want ErrLimited", err)
	}
	if n := calls.Load(); n != geminiAttempts {
		t.Errorf("%d requests, want %d", n, geminiAttempts)
	}
}
