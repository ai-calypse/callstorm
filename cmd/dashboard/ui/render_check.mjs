// Render the calm dashboard's real page script under Node, tab by tab and
// drawer by drawer, against the fixtures in testdata/.
// The failure this catches is the one the dashboard has actually had: a render
// that throws, which in a browser is a page that appears and then goes blank.
//
// Run from cmd/dashboard/ui:  node render_check.mjs
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import React from "react";
import ReactDOMServer from "react-dom/server";
import htm from "htm";

const here = path.dirname(fileURLToPath(import.meta.url));
const page = fs.readFileSync(path.join(here, "index.html"), "utf8");
const scripts = [...page.matchAll(/<script>([\s\S]*?)<\/script>/g)].map(m => m[1]);
const src = scripts.join("\n");
if (!src.includes("function Report(")) throw new Error("did not find the page script");
if (src.includes("@@SHARED@@")) throw new Error("the shared block was never spliced in");

// React reports key and prop problems through console.error and carries on.
const warnings = [];
const realError = console.error;
console.error = (...a) => { warnings.push(String(a[0])); realError(...a); };

const fixture = f => JSON.parse(fs.readFileSync(path.join(here, "testdata", f), "utf8"));
const REFS = JSON.parse(fs.readFileSync(path.join(here, "../../../internal/loadgen/references.json"), "utf8"));

// Enough of a browser for the module to evaluate. Effects never run under
// renderToStaticMarkup, so fetch is never called; it is stubbed so a stray call
// fails loudly rather than reaching the network.
const listeners = {};
globalThis.window = { addEventListener: (k, f) => (listeners[k] = f) };
globalThis.document = { getElementById: () => ({}) };
globalThis.htm = htm;
globalThis.React = React;
globalThis.ReactDOM = { createRoot: () => ({ render() {} }) };
globalThis.fetch = async () => { throw new Error("fetch during render"); };

const mod = new Function(src + "\n;return { Report, Evidence, Method, Findings, RunList, Trend, Sidebar, Glossary, About, Appendix, Sources, TABS, glance, findings, groupRuns, buildQuestions, appendixValues, ComputedAppendix, Questions, ComponentShare, PriorityViews, PreviousTest, matchingTest, comparisonMetrics, validWaits, stageJudged, StudioSummary, StudioTrend, StudioBrief, ScenarioCompare, wilson, filterCalls, judgeIndex };")();
const render = (C, props) => ReactDOMServer.renderToStaticMarkup(React.createElement(C, props));

let bad = 0;
const check = (name, ok) => { if (!ok) bad++; console.log(`${ok ? "ok  " : "FAIL"}  ${name}`); };
const has = (html, name, needle) => check(name, html.includes(needle));

const run = fixture("run.json");
const matrix = fixture("run-matrix.json");
const phases = fixture("run-phases.json");
const index = [run, matrix, phases].map((r, i) => ({
  id: "r" + i, profile: r.profile, scenario: r.scenario, target: r.target, started_at: r.started_at,
  steps: r.steps.length, verdict: ["pass", "warn", "fail"][i], peak_concurrency: 15, worst_p95_ms: 900 + i * 100,
  breakpoint: i === 2 ? "stress at 30 concurrent" : "", judged: i === 1 ? 6 : 0, passed: 5, wer_mean: 0,
}));
const page1 = (rep, id, tab, extra = {}) => render(mod.Report, { rep: { ...rep, id }, id, refs: REFS, runs: index, prev: null, initialTab: tab, onOpenRuns() {}, ...extra });
const drawer = (kind, rep, id, tab) => render(mod.Report, { rep: { ...rep, id }, id, refs: REFS, runs: index, prev: null, initialTab: tab, initialDrawer: kind, onOpenRuns() {} });

// Every tab renders the studio: one chart area, one takeaway, the tabs.
for (const [label, rep] of [["run", run], ["matrix", matrix], ["phases", phases]]) {
  for (const t of mod.TABS) {
    const html = page1(rep, label, t.id);
    check(`${label}/${t.id}: chart area, takeaway and next step`,
      html.includes('class="chart-area"') && html.includes('class="takeaway"') && html.includes("Inspect the evidence"));
    check(`${label}/${t.id}: exactly one selected tab of ${mod.TABS.length}`,
      (html.match(/role="tab"/g) || []).length === mod.TABS.length && (html.match(/aria-selected="true"/g) || []).length === 1);
    const ev = drawer("evidence", rep, label, t.id);
    check(`${label}/${t.id}: evidence drawer renders at least one card`, (ev.match(/class="evidence-block"/g) || []).length >= 1);
    const me = drawer("method", rep, label, t.id);
    check(`${label}/${t.id}: method drawer defines its terms`, me.includes("What these numbers mean") && me.includes("<dt>"));
  }
}

// The page frame the demo has: heading, run picker, summary, tabs, below-note.
{
  const html = page1(run, "run", "load");
  has(html, "page heading", "CALLSTORM / TEST RESULTS");
  has(html, "page heading", "Your assistant, under review.");
  has(html, "run history button", "Run history");
  has(html, "run picker names the run", "Run · run");
  has(html, "at a glance", "AT A GLANCE");
  has(html, "within baseline stat", "Calls handled at once");
  has(html, "task success stat", "Tasks completed");
  has(html, "cost stat", "Cost per call");
  has(html, "explore the evidence", "What would you like to understand?");
  has(html, "how is this measured", "How is this measured?");
  has(html, "view findings", "View findings");
  has(html, "harness note names the check", "Harness suspect on");
  has(html, "compare control", "Previous run");
  const g = mod.glance(run, null, null);
  check("glance headline names a concurrency", /\d+ concurrent/.test(g.headline[0]));
  check("glance counts attention", /check/.test(g.pill));
}

// Load and latency: the chart, the selection, the takeaway, the evidence.
{
  const html = page1(run, "run", "load");
  has(html, "question", "Does the assistant get slower as more people call?");
  has(html, "p95 polyline drawn", "<polyline");
  has(html, "fail line labelled", "2× baseline · fail");
  has(html, "range control", "Inspect load");
  has(html, "selected step observation", "SELECTED STEP");
  has(html, "takeaway is the latency reading", "95 of every 100 turns were faster");
  has(html, "takeaway explains the verdict bands", "past 1.5");
  const pts = (html.match(/class="point"/g) || []).length;
  check(`${pts} clickable points (want ${run.steps.length})`, pts === run.steps.length);

  const ev = drawer("evidence", run, "run", "load");
  has(ev, "report card table", "Every step, as measured");
  has(ev, "component share", "Anatomy of the wait");
  has(ev, "turn anatomy diagram", "transcript of you");
  has(ev, "anatomy names the endpointing half", "listening to silence");
  has(ev, "think/speak share label", "% think");
  has(ev, "reference card", "Where this run sits against what others have published");
  has(ev, "draws the end-of-speech clock", "From true end of speech");
  has(ev, "draws the detection clock", "From detection");
  has(ev, "says none of them decides the verdict", "none of them decides pass or fail");
  has(ev, "integrity empty state", "No pipeline to check on this run");
  has(ev, "what to investigate", "WHAT TO INVESTIGATE");
  // A rect of zero width renders nothing, which is the silent way a bar chart lies.
  const widths = [...ev.matchAll(/<rect[^>]*?\swidth="([\d.]+)"/g)].map(m => Number(m[1]));
  const drawn = widths.filter(w => w > 1).length;
  check(`${drawn} bar segments with real width (want at least ${run.steps.length * 2})`, drawn >= run.steps.length * 2);
  const base = run.steps[0], peak = run.steps[run.steps.length - 1];
  const tsGrew = peak.think_speak.p95_ms - base.think_speak.p95_ms;
  const epGrew = peak.endpointing.p95_ms - base.endpointing.p95_ms;
  if (run.steps.length > 1 && tsGrew > epGrew && tsGrew / base.think_speak.p95_ms >= 0.15) {
    has(ev, "names think/speak as the saturating half", "Think/speak is the half that saturates");
    check("does not also claim the other half", !ev.includes("Endpointing is the half that saturates"));
  }
  const cites = REFS.flatMap(r => r.cites);
  const escaped = s => s.replace(/&/g, "&amp;").replace(/'/g, "&#x27;");
  const named = cites.filter(c => ev.includes(escaped(`${c.source}, ${c.title}`)) && ev.includes(c.published) &&
    (!c.url || ev.includes(`href="${c.url.replace(/&/g, "&amp;")}"`))).length;
  check(`${named} of ${cites.length} citations named with their date`, cites.length > 0 && named === cites.length);

  const me = drawer("method", run, "run", "load");
  has(me, "method defines TTFA in plain words", "how long the line stays dead");
  has(me, "method carries the formula", "value at rank");
  has(me, "method carries the boundary table", "Whose boundary each number spans");
}

// Conversation: the bars, the observation, the evidence tables.
{
  const html = page1(run, "run", "conversation");
  has(html, "question", "Did it sound like a conversation");
  has(html, "80% line", "80% · lecturing");
  has(html, "takeaway is the conversation reading", "visible in a latency chart");
  has(html, "observation names pace", "wpm, caller");
  const ev = drawer("evidence", run, "run", "conversation");
  has(ev, "conversation heading", "How the calls sounded");
  has(ev, "talk ratio cell", "38.9%");
  has(ev, "agent pace cell", "100 wpm");
  has(ev, "interruption score", "5.00");
  has(ev, "dead air column", "none");
}

// Task success on an unjudged run, and on a judged one built from the fixture.
{
  const html = page1(run, "run", "quality");
  has(html, "unjudged chart says so", "was not judged");
  has(html, "unjudged takeaway", "This run was not judged");
  const judged = JSON.parse(JSON.stringify(run));
  judged.judge = {
    backend: "test", judged: 3, passed: 2, errored: 0,
    criteria: ["Asks for the order number", "Offers a replacement first"],
    misses: [{ criterion: "Asks for the order number", judged: 3, missed: 0 }, { criterion: "Offers a replacement first", judged: 3, missed: 1, evidence: "turn 2: \"agent: I can refund that now.\"" }],
    calls: [{ step: "c1", request_id: "aaaa1111", wer: 0.02, ref_words: 50, met: true, judged: true }, { step: "c1", request_id: "bbbb2222", wer: 0.09, ref_words: 50, met: false, judged: true }, { step: "c5", request_id: "cccc3333", wer: 0.01, ref_words: 50, met: true, judged: true }],
    correlation: { calls: 3, threshold: 0.05, clean_calls: 2, clean_passed: 2, misheard_calls: 1, misheard_passed: 0, clean_pass_rate: 1, misheard_pass_rate: 0, gap: 1, conclusive: false, note: "too few judged calls to compare groups", median_wer_passed: 0.015, median_wer_failed: 0.09 },
    judgements: [
      { step: "c1", request_id: "aaaa1111", outcomes: [{ criterion: "Asks for the order number", met: true, evidence: "turn 1: \"agent: What is your order number?\"" }, { criterion: "Offers a replacement first", met: true, evidence: "turn 2: \"agent: I can send a replacement.\"" }] },
      { step: "c1", request_id: "bbbb2222", outcomes: [{ criterion: "Asks for the order number", met: true, evidence: "turn 1: \"agent: Order number please?\"" }, { criterion: "Offers a replacement first", met: false, evidence: "turn 2: \"agent: I can refund that now.\"", reasoning: "The assistant went straight to a refund; no replacement was offered before it." }] },
      { step: "c5", request_id: "cccc3333", outcomes: [{ criterion: "Asks for the order number", met: true, evidence: "turn 1" }, { criterion: "Offers a replacement first", met: true, evidence: "turn 2" }] },
    ],
  };
  const jh = page1(judged, "judged", "quality");
  has(jh, "criteria bars", 'class="scenario"');
  has(jh, "criterion met rate", "67%");
  has(jh, "observation quotes the judge", "Missed at turn 2");
  has(jh, "selected criterion counts the calls that missed it", "1 call missed this criterion");
  has(jh, "missed call is tagged with its step and id", "c1 · bbbb2222");
  has(jh, "missed call carries the judge's reason", "no replacement was offered before it");
  has(jh, "missed call opens its transcript", "Open transcript");
  has(jh, "task success stat is a rate", "66.7");
  has(jh, "headline counts the job", "2 of 3 reviewed calls completed the task.");
  const ev = drawer("evidence", judged, "judged", "quality");
  has(ev, "task card", "Did it actually do the job?");
  has(ev, "heard versus done", "Are the calls it misheard the calls it failed?");
  has(ev, "call log", "3 graded conversations");
  has(ev, "call log quotes evidence", "I can refund that now.");
  has(ev, "call log shows the judge's reasoning", "no replacement was offered before it");
  has(ev, "call log marks the miss", "missed 1 of 2");
  has(ev, "call log points at the transcript file", "judged-calls.jsonl");
  const fi = drawer("findings", judged, "judged", "quality");
  has(fi, "findings list the task reading", "No conclusion to draw yet");
}

// Call logs: the transcripts, turn by turn, with the judge's lines attached.
{
  const calls = [
    { step: run.steps[0].name, request_id: "aaaa1111-0000", turns: [
      { turn: 1, caller_text: "Sure, it's four four eight one two.", agent_text: "Thank you, order four four eight two.", heard_text: "Sure, it's four four eight two.", failed: false, ttfa_ms: 640, endpointing_ms: 220, think_speak_ms: 420, agent_speech_ms: 1800, turn_latency_ms: 2440, pacing_drift_ms: 8 },
      { turn: 2, caller_text: "Can I get a refund instead?", agent_text: "", heard_text: "Can I get a refund instead?", failed: true, fail_reason: "no audio within 8s", ttfa_ms: 0, endpointing_ms: 0, think_speak_ms: 0, agent_speech_ms: 0, turn_latency_ms: 0, pacing_drift_ms: 9 },
    ] },
    { step: run.steps[run.steps.length - 1].name, request_id: "bbbb2222-0000", turns: [
      { turn: 1, caller_text: "Hello?", agent_text: "Hi, how can I help?", heard_text: "Hello?", failed: false, ttfa_ms: 2400, endpointing_ms: 300, think_speak_ms: 2100, agent_speech_ms: 900, turn_latency_ms: 3300, pacing_drift_ms: 5, barged_in: true, barge_in_yield_ms: 410 },
    ] },
    // The newer file shape: a call that never connected, and a turn with an
    // empty heard_text, a node expectation it missed, and a measured round trip.
    { step: run.steps[0].name, request_id: "cccc3333-0000", error: "dial: connection timed out", turns: [] },
    { step: run.steps[0].name, request_id: "dddd4444-0000", turns: [
      { turn: 1, caller_text: "My order number is four one nine two.", agent_text: "Reference reply.", heard_text: "", node: "number", expect_checked: true, expect_met: false, expect_miss: "said none of [\"four one nine two\"]", failed: false, ttfa_ms: 500, endpointing_ms: 200, think_speak_ms: 300, agent_speech_ms: 1000, turn_latency_ms: 1500, pacing_drift_ms: 4, transport_rtt_ms: 12.5, transport_rtt_measured: true },
    ] },
  ];
  const html = page1(run, "run", "calls", { initialCalls: calls });
  has(html, "never-connected call named", "never connected");
  has(html, "never-connected call carries its error", "dial: connection timed out");
  check("empty heard_text is not shown as misheard", !html.includes("heard as: “”"));
  has(html, "node named on the turn", "TURN 1 · number");
  has(html, "missed expectation shown", "Expected reply not met: said none of");
  has(html, "round trip shown when measured", "round trip 13ms");
  has(html, "takeaway counts calls that never connected", "1 call never connected");
  has(html, "call logs question", "What was actually said?");
  has(html, "caller line", "four four eight one two");
  has(html, "heard-as line shows the mishearing", "heard as: “Sure, it&#x27;s four four eight two.”");
  has(html, "agent reply", "order four four eight two");
  has(html, "failed turn named", "no audio within 8s");
  has(html, "dead air marked", "dead air");
  has(html, "barge-in yield shown", "yielded in 410ms");
  has(html, "step filter", "All steps");
  has(html, "search box", "Search what was said");
  has(html, "takeaway counts the calls", "4 calls and 4 turns");
  has(html, "takeaway counts the mishearing", "1 was heard differently");
  has(html, "next step names the slowest turn", "turn 1, 2400ms to first audio");
  has(page1(run, "run", "calls", { initialCalls: null }), "missing calls file says so", "No calls file for this run");
  has(page1(run, "run", "calls"), "loading state", "Loading the calls file");
  const judged = JSON.parse(JSON.stringify(run));
  judged.judge = { backend: "t", judged: 1, passed: 0, errored: 0, criteria: ["Confirms the order number"],
    misses: [{ criterion: "Confirms the order number", judged: 1, missed: 1 }], calls: [], correlation: {},
    judgements: [{ step: run.steps[0].name, request_id: "aaaa1111-0000", outcomes: [{ criterion: "Confirms the order number", met: false, evidence: "turn 1: \"agent: Thank you, order four four eight two.\"" }] }] };
  const jh = page1(judged, "judged", "calls", { initialCalls: calls });
  has(jh, "judge's verdict attached to the turn it quotes", 'class="judged-lines"');
  has(jh, "call badge shows the miss", "missed 1 of 1");
  has(drawer("evidence", judged, "judged", "calls"), "call-log evidence carries the judgements", "1 graded conversations");
  const jx = mod.judgeIndex(judged.judge);
  const ids = outcome => mod.filterCalls(calls, "", "", "", outcome, jx).map(c => c.request_id).join();
  check("failed-call filter: the unconnected call and the one with a failed turn", ids("failed") === "aaaa1111-0000,cccc3333-0000");
  check("missed-task filter: only the graded call that missed", ids("missed") === "aaaa1111-0000");
  check("did-the-job filter: no graded call met every criterion", ids("met") === "");
  has(jh, "outcome filter offered with counts", "Missed the task · 1");
  check("unjudged run offers only the failed-call filter", html.includes("Call failed · 2") && !html.includes("Did the job ·"));

  // The representative slow turn in the latency evidence: the slowest real
  // turn at the selected step, with its stages and its words.
  const ev = render(mod.Report, { rep: { ...run, id: "run" }, id: "run", refs: REFS, runs: index, prev: null, initialTab: "load", initialDrawer: "evidence", initialCalls: calls, onOpenRuns() {} });
  has(ev, "slow turn card", "ONE REPRESENTATIVE SLOW TURN");
  has(ev, "slow turn asks about the selected step", `What happens at ${run.steps[0].concurrency} concurrent call`);
  has(ev, "stage bar drawn", 'class="trace-bar"');
  has(ev, "endpointing row", "Endpointing</span><strong>220ms");
  has(ev, "think/speak row", "Think / speak</span><strong>420ms");
  has(ev, "speaking row", "Agent speaking</span><strong>1800ms");
  has(ev, "total row", "Total turn</span><strong>2440ms");
  has(ev, "the turn's words", "order four four eight two");
  has(ev, "what to investigate", "What to investigate");
  has(ev, "jump to the call log", "Read the whole call in the log");
  // The timeline: every call on the run's own clock, with a recovery step
  // that came back, drawn from the timestamped report shape.
  const timed = JSON.parse(JSON.stringify(run));
  const t0 = Date.parse("2026-09-13T10:00:00Z");
  timed.steps.forEach((s, i) => {
    s.started_at = new Date(t0 + i * 60000).toISOString();
    s.ended_at = new Date(t0 + i * 60000 + 50000).toISOString();
    s.timeline = Array.from({ length: 12 }, (_, k) => ({ at: new Date(t0 + i * 60000 + k * 4000).toISOString(), ttfa_ms: 600 + k * (i === 1 ? 40 : 2), ...(k === 5 && i === 0 ? { failed: true } : {}) }));
  });
  timed.steps[0].timeline.push({ at: new Date(t0 + 49000).toISOString() });
  const rec = timed.steps[timed.steps.length - 1];
  rec.phase = { kind: "recovery", drift: "steady", held_s: 50, back_to_normal: true, back_to_normal_after_s: 12.5, recovered: true };
  const tl = render(mod.Report, { rep: { ...timed, id: "timed" }, id: "timed", refs: REFS, runs: index, prev: null, initialTab: "load", initialDrawer: "evidence", initialCalls: null, onOpenRuns() {} });
  has(tl, "timeline card", "Every call on the run&#x27;s own clock");
  has(tl, "step bands labelled", `${timed.steps[0].name}</text>`);
  has(tl, "recovery marked", "back to normal · 13s");
  has(tl, "a call with no timed turn drawn as a cross", "✕</text>");
  const dots = (tl.match(/<circle[^>]*r="2.6"/g) || []).length;
  check(`${dots} timeline dots (want ${timed.steps.length * 12})`, dots === timed.steps.length * 12);
  has(tl, "timeline reading names the recovery time", "came back 13s after the load dropped");
  has(tl, "timeline surfaces the harness-suspect flag", "these dots measure the");
  has(tl, "band labels clip to their band", "clip-path=\"url(#tl-0)\"");
  const immediate = JSON.parse(JSON.stringify(timed));
  immediate.steps[immediate.steps.length - 1].phase = { kind: "recovery", drift: "steady", held_s: 50, back_to_normal: true, recovered: true };
  has(render(mod.Report, { rep: { ...immediate, id: "im" }, id: "im", refs: REFS, runs: index, prev: null, initialTab: "load", initialDrawer: "evidence", initialCalls: null, onOpenRuns() {} }),
    "a recovery with no delay is read as immediate, not as 0s", "back to normal from the first call after the load dropped");
  check("timeline absent on a run without one", !drawer("evidence", run, "run", "load").includes("Every call on the run&#x27;s own clock"));
  const drifting = JSON.parse(JSON.stringify(timed)); drifting.steps.pop();
  has(render(mod.Report, { rep: { ...drifting, id: "d" }, id: "d", refs: REFS, runs: index, prev: null, initialTab: "load", initialDrawer: "evidence", initialCalls: null, onOpenRuns() {} }),
    "timeline reading catches a step that drifts across its own points", `${timed.steps[1].name} got slower across its own`);

  // The event log and per-turn instants on the newer calls shape.
  const eventful = [{ step: run.steps[0].name, request_id: "eeee5555-0000", started_at: "2026-09-13T10:00:01Z", ended_at: "2026-09-13T10:00:31Z",
    events: [{ t_ms: 0, kind: "connect" }, { t_ms: 1000, kind: "ping", text: "rtt 0.52ms" }, { t_ms: 5600, kind: "caller_end", turn: 1 }, { t_ms: 6102, kind: "first_audio", turn: 1, bytes: 3200 }],
    turns: [{ turn: 1, started_at: "2026-09-13T10:00:01Z", caller_text: "Hi there.", agent_text: "Hello.", heard_text: "Hi there.", failed: false, ttfa_ms: 502, endpointing_ms: 200, think_speak_ms: 302, caller_start_ms: 0, caller_end_ms: 5600, heard_ms: 5800, first_audio_ms: 6102, playout_end_ms: 8102, agent_speech_ms: 2000, turn_latency_ms: 2502, pacing_drift_ms: 3, transport_rtt_ms: 0, transport_rtt_measured: true }] }];
  const eh = page1(run, "run", "calls", { initialCalls: eventful });
  has(eh, "call header carries its start and length", "· 30s");
  has(eh, "event log offered", "4 events on the call&#x27;s clock");
  {
    // A judged run: graded calls carry a verdict, the rest say they were not graded.
    const jrun = JSON.parse(JSON.stringify(run));
    jrun.judge = { backend: "t", judged: 1, passed: 1, errored: 0, criteria: ["c"], misses: [], calls: [], correlation: {},
      judgements: [{ step: run.steps[0].name, request_id: "aaaa1111-0000", outcomes: [{ criterion: "c", met: true, evidence: "turn 1" }] }] };
    const jh = page1(jrun, "jrun", "calls", { initialCalls: calls });
    has(jh, "graded call carries its verdict", "did the job");
    has(jh, "ungraded call says so", "not graded");
  }
  has(eh, "event rows", "rtt 0.52ms");
  has(eh, "event bytes", "3200 bytes");
  has(eh, "per-turn instants on the call's clock", "first audio at 6.10s");
  const callerClock = new Date(Date.parse("2026-09-13T10:00:01Z")).toLocaleTimeString();
  const agentClock = new Date(Date.parse("2026-09-13T10:00:01Z") + 6102).toLocaleTimeString();
  has(eh, "caller line carries its wall-clock time", `CALLER · ${callerClock} · TURN 1`);
  has(eh, "agent reply carries its wall-clock time (first audio)", `AGENT · ${agentClock} · 502ms to first audio`);
  check("a file without instants shows no clock on the turn", page1(run, "run", "calls", { initialCalls: calls }).includes("CALLER · TURN 1"));
  has(eh, "a measured zero round trip is shown as zero", "round trip 0ms");
  const flagOnly = [{ step: run.steps[0].name, request_id: "ffff6666-0000", turns: [{ turn: 1, caller_text: "Hi.", agent_text: "Hello.", failed: false, ttfa_ms: 500, endpointing_ms: 200, think_speak_ms: 300, transport_rtt_measured: true }] }];
  // Older files dropped a real zero through omitempty: the flag alone means 0ms.
  has(page1(run, "run", "calls", { initialCalls: flagOnly }), "a measured flag with no number is read as a zero round trip", "round trip 0ms");
  const evNone = render(mod.Report, { rep: { ...run, id: "run" }, id: "run", refs: REFS, runs: index, prev: null, initialTab: "load", initialDrawer: "evidence", initialCalls: null, onOpenRuns() {} });
  has(evNone, "slow turn absent-file state", "No calls file for this run");
  has(drawer("evidence", run, "run", "load"), "slow turn loading state", "Reading the calls file");
}

// Network on the matrix: the grid in the chart area, and the drawer's commands.
{
  const html = page1(matrix, "matrix", "network");
  has(html, "question", "Does a bad line break it before load does?");
  const cells = (html.match(/class="hm"/g) || []).length;
  const want = matrix.matrix.cohorts.reduce((a, c) => a + (c.steps || []).length, 0);
  check(`${cells} heatmap cells in the chart area (want ${want})`, cells === want);
  has(html, "metric picker", "p95 × clean");
  has(html, "worst cohort observation", "WORST COHORT");
  const ev = drawer("evidence", matrix, "matrix", "network");
  has(ev, "the command as run", "tc qdisc");
  check("node scores rendered or explained", ev.includes("Which part of the conversation gave way") || ev.includes("No node was scored"));
  has(page1(run, "run", "network"), "unimpaired run says so", "unimpaired network");
}

// Cost: the line and the projection to real money.
{
  const html = page1(run, "run", "cost");
  has(html, "question", "Is each call costing more under load?");
  has(html, "cost polyline present", "<polyline");
  has(html, "cost takeaway projects to real money", "a month");
  // The readings are spliced in verbatim; a String.replace once ate their "$$".
  check("cost reading keeps its dollar signs", /A turn cost\s*\$\d/.test(html));
  const ev = drawer("evidence", run, "run", "cost");
  has(ev, "cost card", "Where the billed minutes go");
  has(ev, "cost table", "Per turn");
}

// Pricing from connection time: a run the harness never priced, with calls
// that carry start and end instants, priced at Deepgram's published rate.
{
  const unpriced = JSON.parse(JSON.stringify(run));
  for (const s of unpriced.steps) { delete s.cost; s.calls_attempted = 2; }
  unpriced.started_at = "2026-09-13T16:44:09Z";
  const t0 = Date.parse("2026-09-13T16:44:09Z");
  const calls = unpriced.steps.flatMap((s, i) => [0, 1].map(k => ({
    step: s.name, request_id: `p${i}${k}`, started_at: new Date(t0 + i * 120000 + k * 1000).toISOString(),
    ended_at: new Date(t0 + i * 120000 + k * 1000 + 60000).toISOString(), turns: [] })));
  const html = page1(unpriced, "unpriced", "cost", { initialCalls: calls });
  has(html, "priced from connection time", "priced from each call&#x27;s connection time");
  has(html, "promotional rate in force on the run's date", "$0.056 per minute");
  has(html, "tier picker", "Deepgram tier");
  has(html, "rate is editable", 'type="number"');
  has(html, "cost line drawn from the estimate", "<polyline");
  // 2 calls × 1 minute per step at $0.056: $0.112 a step, $0.056 a call.
  has(html, "per-call figure on the glance", "$0.056");
  has(html, "observation counts connected minutes", "2.0 connected minutes across 2 calls");
  has(html, "takeaway is the cost reading", "a month");
  const ev = render(mod.Report, { rep: { ...unpriced, id: "unpriced" }, id: "unpriced", refs: REFS, runs: index, prev: null, initialTab: "cost", initialDrawer: "evidence", initialCalls: calls, onOpenRuns() {}, onPick() {} });
  has(ev, "evidence card is the estimate", "Priced from connection time");
  has(ev, "evidence cites the source", "deepgram.com/pricing");
  has(ev, "evidence totals the run", `$${(0.112 * unpriced.steps.length).toFixed(2)}`);
  has(page1(unpriced, "unpriced", "cost"), "cost tab waits for the calls file", "Reading the calls file to price this run");
  has(page1(unpriced, "unpriced", "cost", { initialCalls: null }), "no calls file falls back to the unpriced note", "predates cost accounting");
  const later = JSON.parse(JSON.stringify(unpriced)); later.started_at = "2026-10-01T10:00:00Z";
  has(page1(later, "later", "cost", { initialCalls: calls }), "list rate after the promotion ends", "$0.075 per minute");
}

// Phases: the shape table appears in the evidence only when the run has one.
{
  has(drawer("evidence", phases, "phases", "load"), "phase table", "What each phase did over its own duration");
  check("phase table absent on a run of plain ramps", !drawer("evidence", run, "run", "load").includes("What each phase did"));
}

// A run recorded before these fields existed: the empty states say something.
{
  const old = JSON.parse(JSON.stringify(run));
  for (const s of old.steps) { delete s.conversation; delete s.cost; }
  has(page1(old, "old", "conversation"), "legacy run explains missing conversation data", "predates talk ratio");
  has(page1(old, "old", "cost"), "legacy run explains missing cost data", "predates cost accounting");
  has(drawer("evidence", old, "old", "load"), "legacy run still draws the component split", "Anatomy of the wait");
}

// The findings drawer: every reading the run supports, each naming its tab.
{
  const items = mod.findings(run, REFS);
  // The call-log tab reads its takeaway from the calls file, not the report.
  check(`findings cover every report tab (${items.length})`, mod.TABS.filter(t => t.id !== "calls").every(t => items.some(f => f.tab === t.id)));
  const html = drawer("findings", run, "run", "load");
  has(html, "findings heading counts", "things to look at.");
  check("one finding row per finding", (html.match(/class="finding-row"/g) || []).length === items.length);
}

// The run chooser and the frame around it.
{
  const list = render(mod.RunList, { runs: index, sel: "r1", onPick() {} });
  has(list, "runs listed", `${run.profile} · ${run.scenario}`);
  has(list, "selected run marked", "✓ Selected");
  has(list, "verdict badges", "cs-badge-danger");
  has(list, "breakpoint flagged", "breaks at stress at 30 concurrent");
  has(list, "judged flagged", "judged 5/6");
  has(render(mod.Trend, { runs: index }), "trend drawn", "WORST P95 · LAST 3 RUNS");
  has(render(mod.RunList, { runs: [], sel: null, onPick() {} }), "empty history says so", "No runs yet");
  const side = render(mod.Sidebar, { count: 3, target: "ws://agent.example:8080/v1", who: "Yaksh Gandhi", onOverview() {}, onRuns() {}, onAbout() {} });
  has(side, "sidebar nav counts runs", "Test history");
  has(side, "sidebar workspace host", "agent.example:8080");
}

// Suites: several runs shown as one test, in the history and above the report.
{
  const suite = "deepgram-full-test";
  const parts = [
    { id: "s1", suite, profile: "sweep-deepgram-30", scenario: "refund-escalation", started_at: "2026-09-13T16:44:00Z", verdict: "pass", steps: 5, peak_concurrency: 40, worst_p95_ms: 1200, judged: 20, passed: 19, target: "t" },
    { id: "s2", suite, profile: "sweep-deepgram-30", scenario: "refund-bargein", started_at: "2026-09-13T17:10:00Z", verdict: "fail", steps: 5, peak_concurrency: 40, worst_p95_ms: 3100, breakpoint: "c40 at 40 concurrent", judged: 20, passed: 16, target: "t" },
    { id: "s3", suite, profile: "impair-ref", scenario: "refund-escalation", started_at: "2026-09-13T17:40:00Z", verdict: "pass", steps: 2, peak_concurrency: 8, worst_p95_ms: 600, cohorts: 4, network_breaks: ["severe at c2"], harness_degraded: true, target: "t" },
  ];
  const withSuite = [parts[2], parts[1], parts[0], ...index];
  const list = render(mod.RunList, { runs: withSuite, sel: "s2", onPick() {} });
  has(list, "suite folded into one history entry", `Suite · ${suite}`);
  check("suite entry appears once", (list.match(/Suite · deepgram-full-test/g) || []).length === 1);
  has(list, "suite carries the worst verdict", 'class="run-suite selected"');
  has(list, "suite sums judged counts", "judged 35/40");
  has(list, "suite counts parts that found a limit", "2 found a limit");
  has(list, "parts listed in run order", "Part 1 · sweep-deepgram-30 · refund-escalation");
  has(list, "network part listed", "Part 3 · impair-ref");
  const groups = mod.groupRuns(withSuite);
  check(`history has ${groups.length} entries for ${withSuite.length} runs`, groups.length === withSuite.length - 2);

  const rep = { ...run, id: "s2", profile: "sweep-deepgram-30", scenario: "refund-bargein", started_at: "2026-09-13T17:10:00Z" };
  const page = render(mod.Report, { rep, id: "s2", refs: REFS, runs: withSuite, prev: null, onOpenRuns() {}, onPick() {} });
  has(page, "suite strip above the report", `SUITE · ${suite}`);
  has(page, "suite strip counts parts", "3 parts, one test");
  has(page, "suite strip names the limits found", "refund-bargein on sweep-deepgram-30 at c40 at 40 concurrent");
  has(page, "suite strip names the network limit", "impair-ref at severe at c2");
  has(page, "open part marked", "2 · open");
  has(page, "suspect part flagged in the strip", "⚠ harness suspect");
  has(page, "suite reading names the suspect part", "1 of 3 parts drifted past the 100ms pacing limit");
  has(list, "suite entry counts suspect parts", "1 harness suspect");
  has(page, "run picker names the part", "Suite part 2 of 3");
  check("no suite strip on a plain run", !page1(run, "run", "load").includes("SUITE ·"));
}

// The written analysis: a model's reading of the run, every number checked,
// dropped claims counted; on a run above the questions, on a suite under the parts.
{
  const analysis = {
    kind: "run", subject: "run", runs: ["run"], generated_at: "2026-09-13T20:00:00Z", model: "gemini-3.5-flash",
    headline: "The agent holds its baseline to 15 concurrent calls but talks over the caller once in eight turns.",
    insights: [
      { title: "Think/speak grows with load, endpointing does not", category: "latency", severity: "medium",
        evidence: ["think/speak p95 rose from 420ms at c1 to 640ms at c15", "endpointing p95 held between 200ms and 230ms"],
        finding: "The reply, not the listening, is what slows down.", why_it_matters: "A faster silence threshold would change nothing.", next_step: "Stream the voice so audio starts before the sentence is finished." },
      { title: "Every graded call did the job", category: "quality", severity: "info", evidence: ["20 of 20 met every criterion"], finding: "Task success is not the limit here.", why_it_matters: "", next_step: "" },
    ],
    rejected: [{ title: "Setup success fell to 90% at c15", reason: "the data holds 100% setup success at every step" }],
  };
  const html = page1(run, "run", "load", { initialInsights: analysis });
  has(html, "analysis headline", "talks over the caller once in eight turns");
  has(html, "model named", "GEMINI-3.5-FLASH");
  has(html, "insight title", "Think/speak grows with load, endpointing does not");
  has(html, "severity badge", 'class="cs-badge cs-badge-warning">medium');
  has(html, "evidence listed", "endpointing p95 held between 200ms and 230ms");
  has(html, "next step", "Stream the voice so audio starts");
  has(html, "dropped claims counted", "Dropped: 1 claim that could not be checked");
  has(html, "dropped claim reason", "the data holds 100% setup success at every step");
  check("analysis sits above the questions", html.indexOf("talks over the caller") < html.indexOf("What would you like to understand?"));
  has(page1(run, "run", "load", { initialInsights: null }), "no analysis says how to get one", "No written analysis for this run");
  has(page1(run, "run", "load"), "analysis loading state", "Looking for a written analysis");
  // On a suite, the suite's analysis sits under the parts table.
  const suite = "deepgram-full-test";
  const parts = [
    { id: "s1", suite, profile: "sweep", scenario: "a", started_at: "2026-09-13T16:44:00Z", verdict: "pass", steps: 5, peak_concurrency: 40, worst_p95_ms: 1200, target: "t" },
    { id: "s2", suite, profile: "sweep", scenario: "b", started_at: "2026-09-13T17:10:00Z", verdict: "fail", steps: 5, peak_concurrency: 40, worst_p95_ms: 3100, breakpoint: "c40 at 40 concurrent", target: "t" },
  ];
  const sa = { ...analysis, kind: "suite", subject: suite, runs: ["s1", "s2"], headline: "Two of five scenarios break at five concurrent calls.", rejected: [] };
  const sp = render(mod.Report, { rep: { ...run, id: "s2", profile: "sweep", scenario: "b" }, id: "s2", refs: REFS, runs: [parts[1], parts[0], ...index], prev: null, initialInsights: null, initialSuiteInsights: sa, onOpenRuns() {}, onPick() {} });
  has(sp, "suite analysis headline", "Two of five scenarios break at five concurrent calls.");
  check("suite analysis sits under the parts table", sp.indexOf("Two of five scenarios") > sp.indexOf("parts, one test") && sp.indexOf("Two of five scenarios") < sp.indexOf("AT A GLANCE"));
  has(sp, "suite analysis notes its run count", "from 2 runs");
}

// The plain-language questions: the same answers as the classic page, one-liners
// as an index, each opening to the full answer and its chart.
{
  const html = page1(run, "run", "load");
  has(html, "questions section", "questions answered, in plain words");
  has(html, "capacity question asked", "How many calls at the same time can it take before callers notice it getting slower?");
  has(html, "capacity group", "HOW MUCH IT CAN TAKE");
  has(html, "a one-liner answer", "at once");
  has(html, "a full answer", 'class="q-answer"');
  has(html, "a method line", "The slowest callers means the wait that 95 turns in 100 beat");
  has(html, "a chart of the evidence", 'class="qchart"');
  has(html, "an unanswered question says what it would take", "Not answered by this run.");
  has(html, "what the test cannot tell you", "What this kind of test can&#x27;t tell you");
  check("no answer failed to compute", !html.includes("could not be worked out"));
  check("questions sit between the analysis and the evidence tabs",
    html.indexOf("questions answered, in plain words") < html.indexOf("What would you like to understand?"));
  const qs = (html.match(/<details class="q/g) || []).length;
  check("only answered questions appear individually, plus the test limitations", qs === mod.buildQuestions(run, index, "run").filter(q => !q.missing).length + 1);
  has(html, "the newer questions carried over", "Does it get worse at the job before it gets too slow?");
  has(page1(phases, "phases", "load"), "recovery question answered on a run with a recovery step", "After a rush ends, how long does it take to get back to normal?");
}

// Scenario comparison: the parts of a suite read against each other on one axis.
{
  const a = fixture("run-deepgram-graph.json"), b = fixture("run.json");
  const parts = [
    { id: "p1", suite: "suite-x", scenario: a.scenario, profile: a.profile, profile_hash: "h", verdict: "pass", started_at: "2026-09-13T16:00:00Z" },
    { id: "p2", suite: "suite-x", scenario: `${b.scenario}-b`, profile: b.profile, profile_hash: "h", verdict: "fail", breakpoint: "c5 at 5 concurrent", started_at: "2026-09-13T17:00:00Z" },
  ];
  const cmp = render(mod.ScenarioCompare, { parts, reports: { p1: a, p2: b }, onPick() {} });
  for (const t of ["Which scenario keeps callers waiting longest?", "Did every scenario do the job?", "Is the slow moment the same in every scenario?",
    "How often did callers wait long enough to notice?", "Across the suite, are the calls it misheard the calls it failed?", "Everything else, side by side"]) {
    has(cmp, `scenario comparison: ${t}`, t);
  }
  has(cmp, "both scenarios named", `${b.scenario}-b`);
  has(cmp, "task success carries its range", "range 84% to 100%");
  const [lo, hi] = mod.wilson(20, 20);
  check(`20 of 20 reads as a range, not 100% (${lo.toFixed(3)})`, Math.abs(lo - 0.839) < 0.002 && hi === 1);
  check("no interval without a sample", mod.wilson(0, 0) === null);
  has(render(mod.ScenarioCompare, { parts, reports: { p1: a }, onPick() {} }), "a scenario still loading says so", "Loading 1 of 2 scenarios");
  has(render(mod.ScenarioCompare, { parts, reports: { p1: a, p2: null }, onPick() {} }), "a scenario that failed to load is named", "could not be loaded");
  const suiteRuns = parts.map(p => ({ ...p, steps: 5, peak_concurrency: 40, worst_p95_ms: 1000, target: "t" }));
  has(render(mod.Report, { rep: { ...a, id: "p1", suite: "suite-x" }, id: "p1", refs: REFS, runs: suiteRuns, prev: null, onOpenRuns() {}, onPick() {} }), "a suite offers the scenario comparison", "Compare scenarios");
  check("a lone run does not", !page1(run, "run", "load").includes("Compare scenarios"));
}

// Heard versus done: the question and the task view read the same split the
// same way, and say why when a run cannot answer it.
{
  const heardRun = fixture("run-deepgram-graph.json");
  const heardQ = rep => mod.buildQuestions(rep, [], "heard").find(q => q.id === "heard-done");
  const answered = heardQ(heardRun);
  check("heard-versus-done question answered on a run with the split", answered && !answered.missing && /^No/.test(answered.short));
  const tasks = render(mod.PriorityViews, { rep: heardRun, mode: "tasks", onExplore() {}, onCall() {}, onHistory() {} });
  has(tasks, "task view asks the heard-versus-done question", "Are the calls it misheard the calls it failed?");
  has(tasks, "task view draws the strip", "Every judged call by word error rate");
  has(tasks, "task view tables both groups", "Heard cleanly (under");
  has(tasks, "task view reads it the evidence card's way", "Mishearing is not what is failing these calls");
  check("heard-versus-done says why an unjudged run cannot answer", /not judged/.test(heardQ(run).missing || ""));
  const thin = { ...heardRun, judge: { ...heardRun.judge, correlation: { ...heardRun.judge.correlation, conclusive: false, note: "too few judged calls to compare groups" } } };
  check("an inconclusive split is not answered", /Not enough to tell/.test(heardQ(thin).missing || ""));
  has(render(mod.PriorityViews, { rep: run, mode: "tasks", onExplore() {}, onCall() {}, onHistory() {} }), "task view explains a missing split", "no outcome to set hearing against");
}

// The reference sections the page opens with.
{
  const gl = render(mod.Glossary, {});
  has(gl, "glossary card", "What each number catches");
  has(gl, "glossary defines TTFA in plain words", "how long the line stays dead");
  has(gl, "glossary explains p95 vs p50", "worse than 95% of the rest");
  const ab = render(mod.About, {});
  has(ab, "the two questions", "stop meeting its own baseline?");
  has(ab, "the thesis", "stamps every instant on both");
  has(ab, "capabilities", "Prometheus native histograms");
  has(ab, "the foundation", "calibrated against known answers");
  const ap = render(mod.Appendix, { refs: REFS });
  has(ap, "appendix", "How every number is computed");
  has(ap, "boundary table", "Whose boundary each number spans");
  const so = render(mod.Sources, {});
  const sourceRows = (so.match(/<tr>/g) || []).length - 1;
  const sourceLinks = (so.match(/<a class="cs-link" href="https:\/\//g) || []).length;
  check(`${sourceLinks} of ${sourceRows} sources linked`, sourceRows > 0 && sourceLinks === sourceRows);
  check("every cited reference page is listed in sources", REFS.flatMap(r => r.cites).every(c => so.includes(`href="${c.url}"`)));
}


// Values are stage-specific; absent measurements must not become zero.
{
  const v = mod.appendixValues(run, run.steps[0], run.steps, REFS, null, null);
  has(render(mod.ComputedAppendix, {rep:run, refs:REFS}), "calculation selector", 'aria-label="Calculation table test stage"');
  check("TTFA table uses selected stage", v.TTFA.includes(Math.round(run.steps[0].ttfa.p95_ms) + "ms"));
  check("reference table uses numeric percentile and correct clock", !v["Reference line"].includes("measured -"));
  const empty = mod.appendixValues({steps:[{}]}, {}, [{}], [], null, null);
  check("missing RTT stays missing", empty["Round trip"] === "Not measured");
  const zero = mod.appendixValues({steps:[]}, {transport_rtt:{n:2,p50_ms:0,p95_ms:0}}, [], [], null, null);
  check("measured zero RTT remains zero", zero["Round trip"].includes("p95 0ms"));
  const e = {rate:.056,rows:[{name:run.steps[0].name,cost:{usd:.112,agent_minutes:2}}]};
  const unpriced = {...run,steps:run.steps.map(s=>({...s,cost:undefined}))};
  const priced = mod.appendixValues(unpriced, unpriced.steps[0], unpriced.steps, [], e, null);
  check("estimated formula substitutes connection minutes and rate", priced.Cost.includes("2 min × $0.056/min = $0.112"));
  const grouped = render(mod.Questions,{rep:run,runs:index,id:"run"});
  check("missing questions aggregated after answered questions", grouped.indexOf('class="unanswered-questions') > grouped.lastIndexOf('class="q-body"'));
  const qualityRun = {...run, steps:run.steps.map(s => ({...s,quality:{verdict:"pass",compared:["failed turns"]}}))};
  const qualityQuestions = mod.buildQuestions(qualityRun, index, "run");
  check("quality comparisons render with Calm badges", !qualityQuestions.some(q => q.id.startsWith("failed-")));
  const chart = render(mod.ComponentShare,{rep:run});
  check("wait bar segments have visible millisecond labels", /text-anchor="middle"[^>]*>\d+ms<\/text>/.test(chart));
}


{
  const callbacks={onExplore(){},onCall(){},onHistory(){}};
  const rich={...run,scenario_hash:"scenario-v1",profile_hash:"profile-v1",steps:run.steps.map(s=>({...s,
    waits:{turns:10,up_to_800ms:5,"800_to_1200ms":2,"1200_to_2000ms":2,over_2000ms:1},
    turns:[{turn:1,caller_line:"Please check my order",ttfa:{n:10,p50_ms:300,p95_ms:900}}]})),
    judge:{judged:2,passed:1,criteria:["Confirm the request"],backend:"test",calls:[
      {step:run.steps[0].name,judged:true,met:true},{step:run.steps[0].name,judged:false,met:false}],
      misses:[{criterion:"Confirm the request",judged:2,missed:1}],
      judgements:[{request_id:"example-call",outcomes:[{criterion:"Confirm the request",met:false,evidence:"The request was not confirmed."}]}]}};
  const previous={...rich,started_at:"2026-01-01T00:00:00Z"};
  const output=render(mod.PriorityViews,{rep:rich,prev:previous,...callbacks});
  for(const title of ["What changed as more people called?","How often did callers have to wait?","Where in the conversation did things slow down?","Which requirements were missed?","Did the last change help?"]) has(output,"priority view: "+title,title);
  has(output,"task rate excludes unreviewed calls","100.0% \u00b7 1/1");
  has(output,"miss example included","The request was not confirmed.");
  has(output,"heatmap keeps sample counts","10 samples");
  check("matching fingerprints and stage shapes allow comparison",mod.matchingTest(rich,previous)===null);
  check("changed scenario fingerprint prevents comparison",mod.matchingTest(rich,{...previous,scenario_hash:"different"})!==null);
  check("missing fingerprints prevent comparison",mod.matchingTest(run,previous)!==null);
  check("wait percentages use timed replies",mod.comparisonMetrics(rich)[1].value===.3);
  check("incomplete wait bands are not invented",mod.validWaits({waits:{turns:10,up_to_800ms:10}})===null);
  check("different cohorts do not share judged calls",mod.stageJudged(rich,rich.steps[0],"poor-network").length===0);
  check("all empty priority views render",render(mod.PriorityViews,{rep:{steps:[]},...callbacks}).includes("No test stages were recorded"));
}


{
  const summary=render(mod.StudioSummary,{rep:run,g:mod.glance(run,null,null),onExplore(){}});
  has(summary,"studio summary keeps reviewed-call scope","Task review was not recorded");
  has(summary,"glance shows word error rate","Word error rate");
  has(summary,"glance says when WER was not measured","No reference transcript was recorded");
  const graph=fixture("run-deepgram-graph.json");
  has(render(mod.StudioSummary,{rep:graph,g:mod.glance(graph,null,null),onExplore(){}}),"glance pools WER over every stage","571 errors in 11550 caller words");
  has(page1(run,"run"),"overview carries the report card","Every step, as measured");
  const trend=render(mod.StudioTrend,{rep:run,onExplore(){}});
  has(trend,"studio chart has keyboard-operable observations",'tabindex="0"');
  has(trend,"studio chart explains sample count",run.steps[0].ttfa.n+" measured replies");
  has(render(mod.StudioTrend,{rep:{steps:[]},onExplore(){}}),"studio chart empty state","No reply timings were recorded");
  const brief=render(mod.StudioBrief,{rep:run,insights:{insights:[],rejected:[{title:"Do not show this rejected claim"}]},onExplore(){},onAll(){}});
  check("review notes exclude rejected claims",!brief.includes("Do not show this rejected claim"));
}

check(`no React warnings (${warnings.length})`, warnings.length === 0);
for (const w of warnings) console.log("   ", w.slice(0, 300));

console.log(bad === 0 ? "\nall checks passed" : `\n${bad} check(s) failed`);
process.exit(bad === 0 ? 0 : 1);
