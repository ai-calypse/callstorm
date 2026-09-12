package judge

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// ClaudeCode runs the judge through the Claude Code CLI rather than the
// Anthropic API, so a run can be judged on a Claude subscription without API
// credits.
//
// The flags matter as much as the binary. Claude Code is a coding agent: left
// to its defaults it loads its system prompt, tool definitions, MCP servers and
// project settings before answering, which measured 68,835 tokens for a
// two-token question. Replacing the system prompt and disabling tools brings
// the same question down to 925 -- 74 times less, and four times faster. A
// judge is a text-in, text-out job; none of that machinery is wanted.
//
// The cost of this choice is portability: it needs Claude Code installed and
// logged in on the machine running the judge. That is fine on a laptop and no
// good in CI or on a Kubernetes worker, which will want an API client instead.
// Model is the interface precisely so that swap is a constructor change.
type ClaudeCode struct {
	// Bin is the CLI to run. Defaults to "claude" on PATH.
	Bin string

	// Model names the model to judge with. Defaults to Sonnet: the judging is
	// simple, and a smaller model keeps a sampled sweep inside a subscription.
	Model string

	// Timeout bounds one judgement. Defaults to 2 minutes.
	Timeout time.Duration
}

// Complete sends one prompt and returns the model's reply.
func (c ClaudeCode) Complete(ctx context.Context, system, user string) (string, error) {
	bin := c.Bin
	if bin == "" {
		bin = "claude"
	}
	model := c.Model
	if model == "" {
		model = "claude-sonnet-5"
	}
	timeout := c.Timeout
	if timeout == 0 {
		timeout = 2 * time.Minute
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, bin,
		"-p",
		"--output-format", "json",
		"--model", model,
		"--system-prompt", system,
		// No tools, no MCP servers, no settings files: the judge reads the
		// prompt and answers. Anything else is context it pays for and a way
		// for a grader to reach out and touch the machine it is grading on.
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
			return "", fmt.Errorf("judge timed out after %s", timeout)
		}
		return "", fmt.Errorf("run %s: %w: %s", bin, err, strings.TrimSpace(stderr.String()))
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
	return env.Result, nil
}
