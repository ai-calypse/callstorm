# Callstorm · the dashboard

The report cards over every recorded run, for readers unfamiliar with voice
AI testing as much as for the team that placed the calls. Served by
`cmd/dashboard` from this directory, reading the run files through its API.

Open http://localhost:8090/ after starting:

```sh
go run ./cmd/dashboard -runs runs
```

The page opens on a result summary and a median/p95 chart, then separate
views for caller experience, task results, comparing the scenarios of a suite,
comparing with the previous test, findings, and the evidence. Metric cards lead
to the evidence behind them; definitions open from the sidebar.

The overview also includes five views in priority order: combined results by
call load, timed-reply wait bands, an interactive reply-position heatmap,
ranked missed requirements with quoted call evidence, and a previous-test
comparison. Missing measurements remain explicit. Network groups can be
selected independently. Comparisons require matching scenario/profile hashes,
targets, stages and connection conditions; task and cost deltas additionally
require matching review settings and recorded pricing respectively.

The page leads with task completion, four explained results, the sequence
from attempted calls to reviewed tasks, and prioritised next steps. The
written analysis and the question library expand on demand. Six evidence
tabs, transcripts, comparisons and drawers carry every figure the run
recorded. Definitions expand at the bottom.

Missing measurements are explicit. Capacity means the highest tested stage
that passed; reply time is the worst stage's p95, not an average or maximum.
Task completion uses reviewed calls as its denominator. Results cover the
selected run; suite context is separately labelled.

Cost is estimated from connection timestamps as soon as an unpriced report
opens. Unanswered questions are grouped after all answered questions.
The representative slow turn has its own concurrency selector, and the
wait-component bars include millisecond labels. Transcript controls sit above
the calls. Definitions open from the sidebar. The calculation table shows
all appendix formulas with values for a selected stage, marking unavailable
measurements explicitly and identifying whole-run values.

## Files

| File | What it is |
| --- | --- |
| `index.html` | The page: React + htm from a CDN, one script, no build step. |
| `theme.css`, `components.css` | The Callstorm Calm design kit, verbatim from `design-system/callstorm-calm/web/`. Regenerate from `tokens.json`; do not edit here. |
| `dashboard.css`, `clarity.css` | The studio layout and the clear layout on the kit's tokens. |
| `studio.css` | The review layout over those: the summary, the views, and the scenario comparison. |
| `render_check.mjs` | Renders every tab and drawer against the fixtures in `testdata/` under Node. |
| `answers_check.mjs` | Prints one run's plain-language answers beside the numbers they are computed from. |
| `read_run.mjs` | Reads one run's report the way the page does. |

`-export` writes the page and its five stylesheets beside the run files, so
a static host serves the same dashboard.

## Check it

```sh
cd cmd/dashboard/ui
npm install          # once; react, react-dom, htm
node render_check.mjs
```
