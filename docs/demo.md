# Demo: inspect a voice-agent load test

[Open the hosted dashboard](https://ai-calypse.github.io/callstorm/) or run `go run ./cmd/dashboard -runs runs`
and open `http://localhost:8090`. Viewing reports does not place calls or use API keys.

![Recorded dashboard tour](images/callstorm-tour.gif)

The existing 28-second browser recording uses the repository's synthetic reference report to demonstrate
navigation. It is an actual dashboard capture, not a new Deepgram benchmark. [Capture notes](images/README.md).

## Two-minute walkthrough

1. Open the run history and select a report or suite.
2. Compare baseline and peak concurrency. Explain why p95 can deteriorate while p50 stays flat.
3. Open the caller-experience and task-result views; distinguish measured latency from judged success.
4. Inspect the harness-timing caveat and call evidence before accepting a verdict.
5. Open Definitions to check clock boundaries, sample counts, and cited reference lines.

For a deeper code conversation, inspect the [dispatcher](../internal/loadgen/), [worker](../internal/worker/),
and [reference agent](../cmd/refagent/). To reproduce live measurements, use the smoke profile and the setup
in the README; provider calls and synthesis may incur charges. Review transcripts/configuration before publishing.
