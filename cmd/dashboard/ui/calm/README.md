# Callstorm · Evaluation studio (calm view)

The same dashboard as [`../index.html`](../index.html), laid out as the
Callstorm Calm evaluation studio. It reads the same API, carries every card,
reading, glossary entry, formula and boundary row the original has, and
changes nothing about how a run is judged.

## Open it

The Go server embeds everything under `ui/`, so this directory is served
alongside the original with no server change:

```sh
go run ./cmd/dashboard -runs runs
# original:  http://localhost:8090/
# studio:    http://localhost:8090/calm/
```

Every request climbs one directory to `../api/runs.json`,
`../api/runs/{id}.json`, `../api/references.json` and, for the call logs,
`../api/runs/{id}/calls.jsonl` — the calls file recorded beside a report,
which the server now serves and `-export` now copies. `-export` still copies
only the original `index.html`; a static export does not carry this directory.

## How the page is organised

| Region | What it holds |
| --- | --- |
| Sidebar | Overview, the run count (opens the run chooser), and a jump to "What it measures". |
| Heading and run picker | The selected run, its date, steps, calls, turns and duration; "Run history" opens the chooser with the worst-p95 trend and every run's flags. |
| At a glance | Both questions answered in a line each; within-baseline concurrency, task success and cost per call, with deltas against the previous run of the same profile and scenario when one exists. |
| Tabs | Load & latency, Conversation, Task success, Call logs, Network, Cost efficiency — one question, one chart, one selection each. |
| Call logs | Every call, turn by turn: the caller's line, *heard as* when the agent's transcript differs, the agent's reply with its time to first audio, endpointing, think/speak and speech length, failed turns with their reason, barge-ins with the yield time, and the judge's ✓/✕ pinned to the turn it quoted. Filter by step, search any phrase. Read lazily from `calls.jsonl` when the tab opens. |
| Insight panel | The chart, a range/point selector, the takeaway (the card's reading), the selected observation, the suggested next step, and **Inspect the evidence**. |
| How is this measured? | The glossary rows and formulas behind that tab; for latency, the published boundary table. |
| Inspect the evidence | Every original card for that tab: report table, agent share, anatomy of the wait, reference lines, phase shape, harness integrity; conversation, transcript accuracy, barge-in; task success, heard-versus-done and the per-call log with the judge's quoted lines; the impairment grid with the `tc` commands and node scores; the cost table. Each carries "What to investigate". |
| View findings | Every reading the run supports, each opening its evidence. |
| Reference | The full glossary, the two questions and capabilities, the calibration foundation, and the appendix of formulas — always on the page. |

A run recorded before calls files existed shows "No calls file for this run"
on the Call logs tab; every other tab is unaffected.

## Files

| File | What it is |
| --- | --- |
| `index.html` | The page. Reading functions, glossary, appendix and boundary table are copied verbatim from `../index.html`, so both pages say the same thing about the same run. |
| `theme.css`, `components.css` | The design kit, verbatim from `callstorm-calm/web/`. Regenerate from `tokens.json`; do not edit here. |
| `dashboard.css` | The studio layout on the kit's tokens. The added roles are explained at the top of the file. |
| `render_check.mjs` | Renders every tab and drawer against the fixtures in `../testdata/` under Node. |

## Check it

```sh
cd cmd/dashboard/ui
npm install          # once; react, react-dom, htm
node calm/render_check.mjs
```
