package judge

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// DefaultGroqModel is the largest general model on Groq's free tier. Judging a
// five-turn transcript against four criteria is not a hard task, but it is a
// reading-comprehension one, and a small model asked to cite evidence will
// cheerfully cite a turn that does not say what it claims.
const DefaultGroqModel = "openai/gpt-oss-120b"

// groqAttempts bounds how many times one judgement waits out a rate limit.
const groqAttempts = 6

// Groq judges over Groq's OpenAI-compatible API.
//
// It is the counterpart to ClaudeCode: that one spends a Claude subscription
// and needs the CLI installed and logged in, which rules it out of CI and of
// any worker that is not someone's laptop. This one is an HTTP call with a key,
// so it travels.
//
// It also constrains the reply to a JSON schema rather than asking for JSON and
// hoping. A grader that occasionally answers in prose turns into an unjudged
// conversation, and unjudged is the one result that teaches nothing.
type Groq struct {
	APIKey string

	// Model defaults to DefaultGroqModel.
	Model string

	// BaseURL defaults to Groq's public endpoint.
	BaseURL string

	// Timeout bounds one judgement, including any wait for a rate limit to
	// clear. Defaults to 2 minutes.
	Timeout time.Duration

	HTTP *http.Client
}

// outcomesSchema constrains the reply to exactly the verdict shape. Every
// field is required and nothing else is permitted, so a criterion cannot come
// back without the evidence that makes it checkable.
var outcomesSchema = json.RawMessage(`{
  "type": "object",
  "properties": {
    "outcomes": {
      "type": "array",
      "items": {
        "type": "object",
        "properties": {
          "criterion": { "type": "string" },
          "met":       { "type": "boolean" },
          "evidence":  { "type": "string" }
        },
        "required": ["criterion", "met", "evidence"],
        "additionalProperties": false
      }
    }
  },
  "required": ["outcomes"],
  "additionalProperties": false
}`)

// Complete sends one prompt and returns the model's reply.
//
// A rate limit is waited out rather than returned. A sweep's conversations are
// judged back to back, which reaches the free tier's tokens-per-minute ceiling
// within a few calls, and the ceiling clears in seconds: returning at once left
// a judged sweep with most of its conversations unjudged. Groq says how long to
// wait, so that is how long this waits, a bounded number of times; a limit
// that does not clear still fails with its reason.
func (g Groq) Complete(ctx context.Context, system, user string) (string, error) {
	if g.APIKey == "" {
		return "", fmt.Errorf("GROQ_API_KEY is not set")
	}
	model := g.Model
	if model == "" {
		model = DefaultGroqModel
	}
	base := g.BaseURL
	if base == "" {
		base = "https://api.groq.com/openai/v1"
	}
	timeout := g.Timeout
	if timeout == 0 {
		timeout = 2 * time.Minute
	}
	client := g.HTTP
	if client == nil {
		client = &http.Client{}
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	body, err := json.Marshal(map[string]any{
		"model": model,
		"messages": []map[string]string{
			{"role": "system", "content": system},
			{"role": "user", "content": user},
		},
		// Grading the same transcript twice should not produce two answers.
		"temperature": 0,
		"response_format": map[string]any{
			"type": "json_schema",
			"json_schema": map[string]any{
				"name":   "judgement",
				"strict": true,
				"schema": outcomesSchema,
			},
		},
	})
	if err != nil {
		return "", err
	}

	for attempt := 1; ; attempt++ {
		reply, wait, err := g.send(ctx, client, base, body)
		if err != nil && ctx.Err() == context.DeadlineExceeded {
			return "", fmt.Errorf("judge timed out after %s", timeout)
		}
		if err == nil || wait == 0 || attempt == groqAttempts {
			return reply, err
		}
		select {
		case <-time.After(wait):
		case <-ctx.Done():
			return "", fmt.Errorf("judge timed out after %s waiting out a groq rate limit", timeout)
		}
	}
}

// send makes one request. wait is non-zero only for a rate limit, and is how
// long Groq asked to be left alone.
func (g Groq) send(ctx context.Context, client *http.Client, base string, body []byte) (reply string, wait time.Duration, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", 0, err
	}
	req.Header.Set("Authorization", "Bearer "+g.APIKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return "", 0, err
	}
	defer resp.Body.Close()

	var out struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Error struct {
			Message string `json:"message"`
			Type    string `json:"type"`
		} `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", 0, fmt.Errorf("groq %s: decode response: %w", resp.Status, err)
	}

	if resp.StatusCode != http.StatusOK {
		msg := strings.TrimSpace(out.Error.Message)
		if msg == "" {
			msg = resp.Status
		}
		if resp.StatusCode == http.StatusTooManyRequests {
			// Worth naming: the free tier's ceiling is the usual reason a
			// sweep comes back with most of its conversations unjudged.
			return "", retryAfter(resp.Header.Get("Retry-After"), msg),
				fmt.Errorf("groq rate limit: %s (judge fewer calls with -judge-calls, or wait)", msg)
		}
		return "", 0, fmt.Errorf("groq %s: %s", resp.Status, msg)
	}
	if len(out.Choices) == 0 {
		return "", 0, fmt.Errorf("groq returned no choices")
	}
	return out.Choices[0].Message.Content, 0, nil
}

// tryAgainIn reads the wait out of Groq's message, which is more precise than
// its Retry-After header: "Please try again in 3.63s", or "in 1m2.5s".
var tryAgainIn = regexp.MustCompile(`try again in ((?:[0-9.]+(?:ms|h|m|s))+)`)

// retryAfter is how long to wait before trying again: the message's figure,
// else the header's whole seconds, else a few seconds. A little is added,
// because arriving exactly as the window reopens is arriving slightly early.
func retryAfter(header, msg string) time.Duration {
	const margin = 250 * time.Millisecond
	if m := tryAgainIn.FindStringSubmatch(msg); m != nil {
		if d, err := time.ParseDuration(m[1]); err == nil {
			return d + margin
		}
	}
	if s, err := strconv.ParseFloat(strings.TrimSpace(header), 64); err == nil && s > 0 {
		return time.Duration(s*float64(time.Second)) + margin
	}
	return 5 * time.Second
}
