# Callstorm ? Clear results

A separate copy of `../calm/` for readers unfamiliar with voice AI testing.
Uses the same API and scoring functions. The original is untouched.

Open http://localhost:8090/calm/ after starting:

```sh
go run ./cmd/dashboard -runs runs -addr localhost:8090
```

The layout leads with task completion, four explained results, the sequence
from attempted calls to reviewed tasks, and prioritized next steps. Written
analysis and the question library are expandable. All six evidence tabs,
transcripts, comparisons and drawers remain. Definitions expand at the bottom.

Missing measurements are explicit. Capacity means the highest tested stage
that passed; reply time is the worst stage?s p95, not an average or maximum.
Task completion uses reviewed calls as its denominator. Results cover the
selected run; suite context is separately labeled.

`clarity.css` uses the copied Calm tokens. No new runtime dependencies.
The inherited static export does not include this view.

```sh
node cmd/dashboard/ui/calm/render_check.mjs
```
