package insights

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// DefaultModel is Gemini's generally available Flash model: stable, and meant
// for production use, which an analysis written after every run is.
const DefaultModel = "gemini-3.5-flash"

// geminiAttempts bounds how many times one analysis waits out a rate limit.
const geminiAttempts = 4

// ErrLimited is a rate limit that outlasted every attempt. Every attempt counts
// against the quota, and the free tier's is a count of requests, so a batch
// that meets this stops rather than spending the rest of it on refusals.
var ErrLimited = errors.New("Gemini is refusing more requests for now")

// Gemini writes analyses over the Gemini API's generateContent endpoint.
type Gemini struct {
	APIKey string

	// Model defaults to DefaultModel.
	Model string

	// BaseURL defaults to the public endpoint.
	BaseURL string

	// Timeout bounds one analysis, including waits for a rate limit to
	// clear. Defaults to 5 minutes: a suite's data is long, and so is the
	// answer.
	Timeout time.Duration

	HTTP *http.Client
}

// Generate sends one prompt and returns the model's JSON reply.
func (g Gemini) Generate(ctx context.Context, system, user string, schema json.RawMessage) (string, error) {
	if g.APIKey == "" {
		return "", fmt.Errorf("GEMINI_API_KEY is not set")
	}
	model := g.Model
	if model == "" {
		model = DefaultModel
	}
	base := g.BaseURL
	if base == "" {
		base = "https://generativelanguage.googleapis.com/v1beta"
	}
	timeout := g.Timeout
	if timeout == 0 {
		timeout = 5 * time.Minute
	}
	client := g.HTTP
	if client == nil {
		client = &http.Client{}
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	body, err := json.Marshal(map[string]any{
		"systemInstruction": map[string]any{"parts": []map[string]string{{"text": system}}},
		"contents": []map[string]any{
			{"role": "user", "parts": []map[string]string{{"text": user}}},
		},
		"generationConfig": map[string]any{
			"responseMimeType": "application/json",
			"responseSchema":   schema,
		},
	})
	if err != nil {
		return "", err
	}

	url := base + "/models/" + model + ":generateContent"
	for attempt := 1; ; attempt++ {
		text, wait, err := g.send(ctx, client, url, body)
		if err != nil && ctx.Err() == context.DeadlineExceeded {
			return "", fmt.Errorf("gemini timed out after %s", timeout)
		}
		if err != nil && wait != 0 && attempt == geminiAttempts {
			return "", fmt.Errorf("%w: %v", ErrLimited, err)
		}
		if err == nil || wait == 0 {
			return text, err
		}
		select {
		case <-time.After(wait):
		case <-ctx.Done():
			return "", fmt.Errorf("gemini timed out after %s waiting out a rate limit", timeout)
		}
	}
}

// send makes one request. wait is non-zero only when the service asked to be
// tried again later.
func (g Gemini) send(ctx context.Context, client *http.Client, url string, body []byte) (string, time.Duration, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return "", 0, err
	}
	req.Header.Set("x-goog-api-key", g.APIKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return "", 0, err
	}
	defer resp.Body.Close()

	var out struct {
		Candidates []struct {
			Content struct {
				Parts []struct {
					Text    string `json:"text"`
					Thought bool   `json:"thought"`
				} `json:"parts"`
			} `json:"content"`
			FinishReason string `json:"finishReason"`
		} `json:"candidates"`
		PromptFeedback struct {
			BlockReason string `json:"blockReason"`
		} `json:"promptFeedback"`
		Error struct {
			Message string `json:"message"`
			Details []struct {
				Type       string `json:"@type"`
				RetryDelay string `json:"retryDelay"`
			} `json:"details"`
		} `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", 0, fmt.Errorf("gemini %s: decode response: %w", resp.Status, err)
	}

	if resp.StatusCode != http.StatusOK {
		msg := strings.TrimSpace(out.Error.Message)
		if msg == "" {
			msg = resp.Status
		}
		if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode == http.StatusServiceUnavailable {
			// The error names how long to wait when it knows; otherwise a
			// short pause, since both statuses clear on their own.
			wait := 10 * time.Second
			for _, d := range out.Error.Details {
				if strings.HasSuffix(d.Type, "RetryInfo") {
					if w, err := time.ParseDuration(d.RetryDelay); err == nil && w > 0 {
						wait = w + 250*time.Millisecond
					}
				}
			}
			return "", wait, fmt.Errorf("gemini %s: %s", resp.Status, msg)
		}
		return "", 0, fmt.Errorf("gemini %s: %s", resp.Status, msg)
	}
	if out.PromptFeedback.BlockReason != "" {
		return "", 0, fmt.Errorf("gemini refused the prompt: %s", out.PromptFeedback.BlockReason)
	}
	if len(out.Candidates) == 0 {
		return "", 0, fmt.Errorf("gemini returned no candidates")
	}

	c := out.Candidates[0]
	var b strings.Builder
	for _, p := range c.Content.Parts {
		if !p.Thought {
			b.WriteString(p.Text)
		}
	}
	if c.FinishReason == "MAX_TOKENS" {
		return "", 0, fmt.Errorf("gemini stopped at its output limit before finishing the analysis")
	}
	if b.Len() == 0 {
		return "", 0, fmt.Errorf("gemini returned no text (finish reason %s)", c.FinishReason)
	}
	return b.String(), 0, nil
}
