package judge

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// A sweep judged back to back reaches the free tier's ceiling in a few calls.
// The judge waits as long as Groq asks and tries again, rather than recording
// the conversation as unjudged.
func TestGroqWaitsOutARateLimit(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) <= 2 {
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"error":{"message":"Rate limit reached. Please try again in 20ms. Need more tokens?"}}`))
			return
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"{\"outcomes\":[]}"}}]}`))
	}))
	defer srv.Close()

	got, err := Groq{APIKey: "k", BaseURL: srv.URL, Timeout: 10 * time.Second}.Complete(context.Background(), "s", "u")
	if err != nil {
		t.Fatalf("rate limit was not waited out: %v", err)
	}
	if got != `{"outcomes":[]}` {
		t.Errorf("reply %q", got)
	}
	if n := calls.Load(); n != 3 {
		t.Errorf("made %d requests, want 3: two limited, one answered", n)
	}
}

// A limit that does not clear still fails, with the reason, after a bounded
// number of tries rather than waiting forever.
func TestGroqGivesUpOnALimitThatDoesNotClear(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":{"message":"Please try again in 1ms."}}`))
	}))
	defer srv.Close()

	_, err := Groq{APIKey: "k", BaseURL: srv.URL, Timeout: 10 * time.Second}.Complete(context.Background(), "s", "u")
	if err == nil || !strings.Contains(err.Error(), "rate limit") {
		t.Fatalf("err = %v, want a rate limit error", err)
	}
	if n := calls.Load(); n != groqAttempts {
		t.Errorf("made %d requests, want %d", n, groqAttempts)
	}
}

func TestRetryAfterReadsGroqsMessageBeforeTheHeader(t *testing.T) {
	for _, c := range []struct {
		header, msg string
		want        time.Duration
	}{
		{"4", "Please try again in 3.63s. Need more tokens?", 3630*time.Millisecond + 250*time.Millisecond},
		{"", "Please try again in 1m2.5s.", 62500*time.Millisecond + 250*time.Millisecond},
		{"7", "Rate limit reached.", 7*time.Second + 250*time.Millisecond},
		{"", "Rate limit reached.", 5 * time.Second},
	} {
		if got := retryAfter(c.header, c.msg); got != c.want {
			t.Errorf("retryAfter(%q, %q) = %s, want %s", c.header, c.msg, got, c.want)
		}
	}
}
