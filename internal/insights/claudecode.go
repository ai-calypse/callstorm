package insights

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// DefaultClaudeModel writes analyses through Claude Code. Reading a whole suite
// for patterns across scenarios is the hard part of this job, so it is the
// larger model, where the judge's simpler task takes the smaller one.
const DefaultClaudeModel = "claude-opus-5"

// ClaudeCode writes analyses through the Claude Code CLI on a Claude
// subscription, with no API key: the way to keep analyses current when
// Gemini's free tier has run out.
//
// The CLI cannot constrain a reply to a schema, so the schema goes into the
// system prompt, and a reply that is not the JSON asked for fails the analysis
// rather than being guessed at. Every number in it is still checked against
// the data by Generate, whichever model wrote it.
type ClaudeCode struct {
	// Bin is the CLI to run. Defaults to "claude" on PATH.
	Bin string

	// Model defaults to DefaultClaudeModel.
	Model string

	// Timeout bounds one analysis. Defaults to 10 minutes: a suite's data is
	// long, and so is the answer.
	Timeout time.Duration
}

// Generate sends one prompt and returns the model's JSON reply.
func (c ClaudeCode) Generate(ctx context.Context, system, user string, schema json.RawMessage) (string, error) {
	bin := c.Bin
	if bin == "" {
		bin = "claude"
	}
	model := c.Model
	if model == "" {
		model = DefaultClaudeModel
	}
	timeout := c.Timeout
	if timeout == 0 {
		timeout = 10 * time.Minute
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	system += "\n\nReply with one JSON object only, with no prose and no code fence, matching this schema. OBJECT, ARRAY and STRING are JSON types, and properties come in the order listed:\n" + string(schema)
	cmd := exec.CommandContext(ctx, bin,
		"-p",
		"--output-format", "json",
		"--model", model,
		"--system-prompt", system,
		// No tools, no MCP servers, no settings files, as for the judge: the
		// analysis reads the data it is given and nothing else.
		"--tools", "",
		"--strict-mcp-config",
		"--setting-sources", "",
	)
	cmd.Stdin = strings.NewReader(user)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return "", fmt.Errorf("claude code timed out after %s", timeout)
		}
		// The CLI reports a refusal such as a usage limit on stdout, in its JSON
		// envelope, and leaves stderr empty.
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = strings.TrimSpace(stdout.String())
		}
		return "", fmt.Errorf("run %s: %w: %s", bin, err, msg)
	}

	var env struct {
		Result  string `json:"result"`
		IsError bool   `json:"is_error"`
		Subtype string `json:"subtype"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &env); err != nil {
		return "", fmt.Errorf("parse %s output: %w", bin, err)
	}
	if env.IsError {
		return "", fmt.Errorf("%s reported an error (%s): %s", bin, env.Subtype, env.Result)
	}
	return unfence(env.Result), nil
}

// unfence tolerates a reply wrapped in a code fence despite being asked not to.
func unfence(s string) string {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "```") {
		return s
	}
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[i+1:]
	}
	if i := strings.LastIndex(s, "```"); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}
