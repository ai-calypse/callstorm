// Render the dashboard's real page script under Node and assert the new cards
// appear. This is the check that catches the failure mode the dashboard has
// actually had: a render that throws on a prop React silently rejects, which in
// a browser shows as a page that appears and then goes blank.
import fs from "node:fs";
import path from "node:path";
import React from "react";
import ReactDOMServer from "react-dom/server";
import htm from "htm";

const HTML = process.argv[2] || "index.html";
const RUN = process.argv[3] || "testdata/run.json";

const page = fs.readFileSync(HTML, "utf8");
const scripts = [...page.matchAll(/<script>([\s\S]*?)<\/script>/g)].map(m => m[1]);
const src = scripts.join("\n");
if (!src.includes("function ReportCard")) throw new Error("did not find the page script");

// React reports key and prop problems through console.error and carries on.
// Those are the warnings that precede the bugs this dashboard has actually
// had, so they fail the check rather than scrolling past.
const warnings = [];
const realError = console.error;
console.error = (...a) => { warnings.push(String(a[0])); realError(...a); };

const run = JSON.parse(fs.readFileSync(RUN, "utf8"));
const REFS = JSON.parse(fs.readFileSync("../../../internal/loadgen/references.json", "utf8"));
const id = path.basename(RUN, ".json");
const index = [{
  id, profile: run.profile, scenario: run.scenario, target: run.target,
  started_at: run.started_at, duration_s: run.duration_s, steps: run.steps.length,
  verdict: run.verdict || "pass", peak_concurrency: 15,
  baseline_p95_ms: 762.6, worst_p95_ms: 1027.9, wer_mean: 0, harness_degraded: true,
}];

// Enough of a browser for the module to evaluate. fetch is the only thing the
// page actually needs from the environment.
const listeners = {};
globalThis.window = { addEventListener: (k, f) => (listeners[k] = f) };
globalThis.document = { getElementById: () => ({}) };
globalThis.htm = htm;
globalThis.React = React;
globalThis.ReactDOM = { createRoot: () => ({ render() {} }) };
globalThis.fetch = async (url) => ({
  ok: true,
  json: async () => (url.includes("/runs/") ? run : index),
});

const mod = new Function(src + "\n;return { App, ReportCard, ComponentShare, CostChart, ReferenceLines, placeRef, PhaseTable, TaskSuccessCard, ImpairmentHeatmap, NodeScores, IntegrityCard, Appendix, AgentLatency };")();

// useEffect does not run during renderToString, so the components are driven
// directly with the data the fetch would have produced. That is the point:
// this asserts the render path, not the plumbing the browser already proves.
const html = ReactDOMServer.renderToStaticMarkup(
  React.createElement(mod.ComponentShare, { steps: run.steps })) +
  ReactDOMServer.renderToStaticMarkup(
  React.createElement(mod.CostChart, { steps: run.steps }));

const want = [
  ["component share heading", "Which half saturates first"],
  ["cost heading", "What the latency costs"],
  ["stacked bars present", "<rect"],
  ["cost polyline present", "<polyline"],
  ["think/speak share label", "% think"],
];
let bad = 0;
for (const [name, needle] of want) {
  const ok = html.includes(needle);
  if (!ok) bad++;
  console.log(`${ok ? "ok  " : "FAIL"}  ${name}`);
}

// A rect of zero width renders nothing, which is the silent way a bar chart
// lies: the card is there, the bars are not.
const widths = [...html.matchAll(/<rect[^>]*width="([\d.]+)"/g)].map(m => Number(m[1]));
const drawn = widths.filter(w => w > 1).length;
console.log(`${drawn >= run.steps.length * 2 ? "ok  " : "FAIL"}  ${drawn} bar segments with real width (want ${run.steps.length * 2})`);
if (drawn < run.steps.length * 2) bad++;

// Now the whole ReportCard, including the conversation table. useEffect never
// fires server-side, so the state hooks are seeded in call order instead: the
// first useState in ReportCard is the report, the second is the error.
let seeded = 0;
let current = run;
const stub = Object.create(React);
stub.useState = () => [seeded++ === 0 ? current : null, () => {}];
stub.useEffect = () => {};
globalThis.React = stub;
const mod2 = new Function(src + "\n;return { ReportCard };")();
const card = ReactDOMServer.renderToStaticMarkup(
  React.createElement(mod2.ReportCard, { refs: REFS, id }));

const wantCard = [
  ["conversation heading", "How the calls sounded"],
  ["talk ratio cell", "38.9%"],
  ["agent pace cell", "100 wpm"],
  ["interruption score", "5.00"],
  ["dead air column", "none"],
  ["component share inside the card", "Which half saturates first"],
  ["cost chart inside the card", "What the latency costs"],
];
for (const [name, needle] of wantCard) {
  const ok = card.includes(needle);
  if (!ok) bad++;
  console.log(`${ok ? "ok  " : "FAIL"}  ${name}`);
}

// The teaching layer: a glossary before any table, a diagram of the split, and
// a computed reading under every chart. These are the parts that turn a page of
// numbers into something a reader who has never load-tested an agent can use,
// so they are asserted like any other output.
const wantTeaching = [
  ["glossary card", "What each number catches"],
  ["glossary defines TTFA in plain words", "how long the line stays dead"],
  ["glossary explains p95 vs p50", "worse than 95% of the rest"],
  ["turn anatomy diagram", "transcript of you"],
  ["anatomy names the endpointing half", "listening to silence"],
  ["anatomy names the think/speak half", "composes a reply"],
  ["latency reading present", "95 of every 100 turns were faster"],
  ["latency reading explains the verdict bands", "past 1.5"],
  ["conversation reading present", "visible in a latency chart"],
  ["cost reading projects to real money", "a month"],
  ["reading says what to do", "read-do"],
];
for (const [name, needle] of wantTeaching) {
  const ok = card.includes(needle);
  if (!ok) bad++;
  console.log(`${ok ? "ok  " : "FAIL"}  ${name}`);
}

// The reading has to be right, not merely present. This fixture's think/speak
// grew while endpointing held, so there is exactly one correct verdict.
const base = run.steps[0], peak = run.steps[run.steps.length - 1];
const tsGrew = peak.think_speak.p95_ms - base.think_speak.p95_ms;
const epGrew = peak.endpointing.p95_ms - base.endpointing.p95_ms;
if (run.steps.length > 1 && tsGrew > epGrew && tsGrew / base.think_speak.p95_ms >= 0.15) {
  const right = card.includes("Think/speak is the half that saturates");
  if (!right) bad++;
  console.log(`${right ? "ok  " : "FAIL"}  names think/speak as the saturating half (it grew ${Math.round(tsGrew)}ms vs endpointing ${Math.round(epGrew)}ms)`);
  const wrong = card.includes("Endpointing is the half that saturates");
  if (wrong) bad++;
  console.log(`${!wrong ? "ok  " : "FAIL"}  does not also claim the other half`);
}

// The same card against a run recorded before these fields existed. A card
// that hides itself when its data is absent is indistinguishable from a card
// that is broken, and every run already on disk is this shape -- so the empty
// state is the case that actually ships, and it has to say something.
const old = JSON.parse(JSON.stringify(run));
for (const s of old.steps) { delete s.conversation; delete s.cost; }
seeded = 0;
current = old;
const legacy = ReactDOMServer.renderToStaticMarkup(
  React.createElement(mod2.ReportCard, { refs: REFS, id }));

const wantLegacy = [
  ["legacy run explains missing conversation data", "predates talk ratio"],
  ["legacy run explains missing cost data", "predates cost accounting"],
  ["legacy run still draws the component split", "Which half saturates first"],
];
for (const [name, needle] of wantLegacy) {
  const ok = legacy.includes(needle);
  if (!ok) bad++;
  console.log(`${ok ? "ok  " : "FAIL"}  ${name}`);
}


// Reference lines on two clocks, rendered from the fixture's own steps against
// the real published list. A line whose citation or clock goes missing fails
// here rather than in front of a reader.
const refsHtml = ReactDOMServer.renderToStaticMarkup(
  React.createElement(mod.ReferenceLines, { steps: run.steps, refs: REFS }));

for (const [name, needle] of [
  ["reference card", "Where this run sits against what others have published"],
  ["draws the end-of-speech clock", "From true end of speech"],
  ["draws the detection clock", "From detection"],
  ["says none of them decides the verdict", "none of them decides pass or fail"],
  ["starts from where real agents are", "Start from where real agents are"],
  ["an observed range is shaded", "<rect"],
]) {
  const ok = refsHtml.includes(needle);
  if (!ok) bad++;
  console.log(`${ok ? "ok  " : "FAIL"}  ${name}`);
}
{
  // Every citation is named with its date, and linked wherever a URL is known.
  const cites = REFS.flatMap(r => r.cites);
  const named = cites.filter(c => refsHtml.includes(`${c.source}, ${c.title}`) && refsHtml.includes(c.published) &&
    (!c.url || refsHtml.includes(`href="${c.url.replace(/&/g, "&amp;")}"`))).length;
  const ok = cites.length > 0 && named === cites.length;
  if (!ok) bad++;
  console.log(`${ok ? "ok  " : "FAIL"}  ${named} of ${cites.length} citations named with their date`);

  // Every step on both clocks: a p50 and a p95 dot per clock with samples.
  const want = run.steps.filter(s => s.ttfa && s.ttfa.n > 0)
    .reduce((a, s) => a + ["ttfa", "think_speak"].filter(k => s[k] && s[k].n > 0).length * 2, 0);
  const dots = (refsHtml.match(/<circle/g) || []).length;
  const drawn = dots === want;
  if (!drawn) bad++;
  console.log(`${drawn ? "ok  " : "FAIL"}  ${dots} dots for ${want} step, clock and percentile readings`);
}
{
  const none = ReactDOMServer.renderToStaticMarkup(
    React.createElement(mod.ReferenceLines, { steps: run.steps, refs: [] }));
  const ok = none.includes("No reference lines loaded");
  if (!ok) bad++;
  console.log(`${ok ? "ok  " : "FAIL"}  a page without the list says so`);
}

// Phase shape. A plain ramp has nothing to say and must say nothing: a card
// that renders an empty table is worse than one that does not render.
const plainRamp = ReactDOMServer.renderToStaticMarkup(
  React.createElement(mod.PhaseTable, {
    steps: [{ name: "c1", concurrency: 1, phase: { kind: "ramp", drift: "unknown", held_s: 0 } }],
  })) || "";
{
  const ok = plainRamp === "";
  if (!ok) bad++;
  console.log(`${ok ? "ok  " : "FAIL"}  a ramp with no shape renders no phase card`);
}

// A soak that drifted is the finding the whole phase model exists for, so the
// card has to name the step, quote both halves, and say what to look for.
const drifted = ReactDOMServer.renderToStaticMarkup(
  React.createElement(mod.PhaseTable, {
    steps: [
      { name: "ramp-c4", concurrency: 4, p95_ratio: 1,
        phase: { kind: "ramp", drift: "steady", drift_pct: 0.02, held_s: 120,
                 first_half: { n: 40, p50_ms: 410 }, second_half: { n: 40, p50_ms: 418 } } },
      { name: "soak-c8", concurrency: 8, p95_ratio: 1.3,
        phase: { kind: "soak", drift: "drifting", drift_pct: 0.42, held_s: 600,
                 first_half: { n: 60, p50_ms: 430 }, second_half: { n: 60, p50_ms: 610 } } },
      { name: "recover-c4", concurrency: 4, p95_ratio: 1.9,
        phase: { kind: "recovery", drift: "steady", drift_pct: 0.01, held_s: 120, recovered: false,
                 first_half: { n: 40, p50_ms: 700 }, second_half: { n: 40, p50_ms: 707 } } },
    ],
  }));

for (const [name, needle] of [
  ["phase card", "over its own duration"],
  ["names the drifting step", "soak-c8"],
  ["quotes both halves", "610ms"],
  ["explains what a soak is for", "cache going"],
  ["points at accumulation, not capacity", "adding capacity will not fix it"],
  ["says why the median and not p95", "one unlucky call"],
]) {
  const ok = drifted.includes(needle);
  if (!ok) bad++;
  console.log(`${ok ? "ok  " : "FAIL"}  ${name}`);
}

// A recovery that never came back must not be reported as a drift finding it
// is not, nor swallowed by the drift branch above it.
const stuck = ReactDOMServer.renderToStaticMarkup(
  React.createElement(mod.PhaseTable, {
    steps: [
      { name: "recover-c4", concurrency: 4, p95_ratio: 1.9,
        phase: { kind: "recovery", drift: "steady", drift_pct: 0.01, held_s: 120, recovered: false,
                 first_half: { n: 40, p50_ms: 700 }, second_half: { n: 40, p50_ms: 707 } } },
    ],
  }));
for (const [name, needle] of [
  ["recovery failure is the headline when nothing drifted", "It did not come back"],
  ["says what the peak might have left behind", "still draining"],
]) {
  const ok = stuck.includes(needle);
  if (!ok) bad++;
  console.log(`${ok ? "ok  " : "FAIL"}  ${name}`);
}

// Task success. The empty state is the case that ships on every unjudged run,
// so it has to say what is missing and how to get it.
const nojudge = ReactDOMServer.renderToStaticMarkup(
  React.createElement(mod.TaskSuccessCard, { judge: undefined }));
for (const [name, needle] of [
  ["unjudged run says so", "was not judged"],
  ["and says how to fix that", "-judge"],
]) {
  const ok = nojudge.includes(needle);
  if (!ok) bad++;
  console.log(`${ok ? "ok  " : "FAIL"}  ${name}`);
}

// A judged run where mishearing clearly separated the failures. The numbers
// are chosen so there is exactly one correct verdict.
const judged = {
  backend: "groq test-model",
  criteria: ["The agent asks for an order number", "The agent offers a replacement first"],
  judged: 10, passed: 5, errored: 1,
  misses: [
    { criterion: "The agent asks for an order number", missed: 0, judged: 10 },
    { criterion: "The agent offers a replacement first", missed: 5, judged: 10,
      evidence: "turn 3: \"I can refund that for you right away.\"" },
  ],
  calls: [
    { step: "c1", request_id: "a", wer: 0.01, ref_words: 71, met: true, judged: true },
    { step: "c1", request_id: "b", wer: 0.02, ref_words: 71, met: true, judged: true },
    { step: "c5", request_id: "c", wer: 0.02, ref_words: 71, met: true, judged: true },
    { step: "c5", request_id: "d", wer: 0.03, ref_words: 71, met: true, judged: true },
    { step: "c10", request_id: "e", wer: 0.04, ref_words: 71, met: true, judged: true },
    { step: "c10", request_id: "f", wer: 0.09, ref_words: 71, met: false, judged: true },
    { step: "c20", request_id: "g", wer: 0.11, ref_words: 71, met: false, judged: true },
    { step: "c20", request_id: "h", wer: 0.13, ref_words: 71, met: false, judged: true },
    { step: "c40", request_id: "i", wer: 0.18, ref_words: 71, met: false, judged: true },
    { step: "c40", request_id: "j", wer: 0.24, ref_words: 71, met: false, judged: true },
  ],
  correlation: {
    calls: 10, threshold: 0.05,
    clean_calls: 5, clean_passed: 5, misheard_calls: 5, misheard_passed: 0,
    clean_pass_rate: 1, misheard_pass_rate: 0, gap: 1,
    median_wer_passed: 0.02, median_wer_failed: 0.13,
    conclusive: true, note: "",
  },
};
const task = ReactDOMServer.renderToStaticMarkup(
  React.createElement(mod.TaskSuccessCard, { judge: judged }));

for (const [name, needle] of [
  ["task card", "Did it actually do the job"],
  ["names the grader", "groq test-model"],
  ["lists the criteria", "offers a replacement first"],
  ["quotes the judge's evidence", "refund that for you right away"],
  ["warns the judge only reads words", "measured"],
  ["draws the strip plot", "did the job"],
  ["marks the usable threshold", "usable"],
  ["one dot per judged call", "<circle"],
  ["two-group table", "Heard cleanly"],
  ["names the finding", "Mishearing is costing it the task"],
  ["and the fix", "Fix the listening before the reasoning"],
]) {
  const ok = task.includes(needle);
  if (!ok) bad++;
  console.log(`${ok ? "ok  " : "FAIL"}  ${name}`);
}
{
  // Ten judged calls, ten dots. A strip plot that quietly drops points is the
  // silent way this chart lies.
  const dots = (task.match(/<circle/g) || []).length;
  const ok = dots === judged.calls.length;
  if (!ok) bad++;
  console.log(`${ok ? "ok  " : "FAIL"}  ${dots} dots for ${judged.calls.length} judged calls`);
  // And the opposite verdict must not also appear.
  const both = task.includes("Mishearing is not what is failing");
  if (both) bad++;
  console.log(`${!both ? "ok  " : "FAIL"}  does not also claim the opposite`);
}

// The same card with a group empty, which is what a real clean run produces.
// It has to report the numbers and withhold the conclusion, not invent one.
const thin = JSON.parse(JSON.stringify(judged));
thin.correlation = { calls: 7, threshold: 0.05, clean_calls: 7, clean_passed: 7,
  misheard_calls: 0, misheard_passed: 0, clean_pass_rate: 1, misheard_pass_rate: 0, gap: 0,
  median_wer_passed: 0.028, median_wer_failed: 0, conclusive: false,
  note: "too few judged calls to compare groups" };
thin.calls = judged.calls.slice(0, 5).map(c => ({ ...c, met: true }));
const thinHtml = ReactDOMServer.renderToStaticMarkup(
  React.createElement(mod.TaskSuccessCard, { judge: thin }));
for (const [name, needle] of [
  ["an inconclusive split says so", "No conclusion to draw yet"],
  ["and quotes the reason", "too few judged calls"],
  ["and says how to get a conclusion", "judge-calls"],
]) {
  const ok = thinHtml.includes(needle);
  if (!ok) bad++;
  console.log(`${ok ? "ok  " : "FAIL"}  ${name}`);
}
{
  const claims = thinHtml.includes("Mishearing is costing it the task");
  if (claims) bad++;
  console.log(`${!claims ? "ok  " : "FAIL"}  draws no conclusion from an inconclusive split`);
}

// A second fixture: a real six-phase run against the reference agent, where
// the injected latency is known in advance. It is here because the first
// fixture predates the phase model and the known-answer placements, so every assertion
// about those would have passed by never running -- the same shape of
// non-check that let the conversation card ship hidden.
//
// The reference agent adds 25ms per caller past a capacity of 6, so the bands
// each step lands in are arithmetic rather than opinion: 4 concurrent stays
// near 400ms and 24 concurrent reaches 850ms.
const phased = JSON.parse(fs.readFileSync("testdata/run-phases.json", "utf8"));
seeded = 0;
current = phased;
const phasedCard = ReactDOMServer.renderToStaticMarkup(
  React.createElement(mod2.ReportCard, { refs: REFS, id: "run-phases" }));

for (const [name, needle] of [
  ["reference card renders inside the report", "Where this run sits against what others have published"],
  ["phase card renders inside the report", "over its own duration"],
  ["spike phase is named", ">spike<"],
  ["soak phase is named", ">soak<"],
  ["recovery phase is named", ">recovery<"],
  ["recovery that came back says so", "back to baseline"],
  ["a step too short for a shape says that, not steady", "too short to tell"],
  ["task card offers the missing verdict", "was not judged"],
]) {
  const ok = phasedCard.includes(needle);
  if (!ok) bad++;
  console.log(`${ok ? "ok  " : "FAIL"}  ${name}`);
}

// The known answer, restated against the published lines. The reference agent
// was given a capacity of 6 and 25ms per caller past it, so from end of speech
// the baseline ramp sits near 400ms and the spike near 850ms: both past the
// human marker and both under the production median, which is arithmetic
// rather than opinion.
{
  const human = REFS.find(r => r.kind === "perceptual");
  const observed = REFS.find(r => r.kind === "observed");
  const spike = phased.steps.find(s => s.phase && s.phase.kind === "spike");
  const ramp = phased.steps.find(s => s.name === phased.baseline);
  const p50 = (r, s) => mod.placeRef(r, s).find(m => m.percentile === 50);
  const right = human && observed && spike && ramp &&
    p50(human, ramp).over && p50(human, spike).over &&
    p50(observed, ramp).value_ms < observed.ms && p50(observed, spike).value_ms < observed.ms;
  if (!right) bad++;
  console.log(`${right ? "ok  " : "FAIL"}  the injected latency lands where it should against the published lines (${ramp && Math.round(ramp.ttfa.p50_ms)}ms then ${spike && Math.round(spike.ttfa.p50_ms)}ms)`);
}

// The same run with a judge block attached, to prove the task card mounts
// inside the whole page and not only when rendered on its own.
const withJudge = JSON.parse(JSON.stringify(phased));
withJudge.judge = judged;
seeded = 0;
current = withJudge;
const judgedCard = ReactDOMServer.renderToStaticMarkup(
  React.createElement(mod2.ReportCard, { refs: REFS, id: "run-judged" }));
for (const [name, needle] of [
  ["task card mounts in the full report", "Did it actually do the job"],
  ["and brings its finding with it", "Mishearing is costing it the task"],
]) {
  const ok = judgedCard.includes(needle);
  if (!ok) bad++;
  console.log(`${ok ? "ok  " : "FAIL"}  ${name}`);
}

// The impairment matrix, the scenario nodes, harness integrity and the
// appendix. Every run already on disk predates all four, so the empty states
// are what ships first and are asserted as closely as the populated ones.
for (const [name, needle] of [
  ["an unimpaired run says so", "Placed on an unimpaired network"],
  ["and says how to impair one", "60-impair-job.yaml"],
  ["a run with no pipeline says there was nothing to check", "No pipeline to check"],
  ["and that it is not the target's webhooks", "not the target&#x27;s webhooks"],
  ["a run before node scoring says so", "predates per-node scoring"],
  ["a run before round-trip measurement says so", "predates round-trip measurement"],
  ["the appendix mounts in the report", "How every number is computed"],
  ["the appendix gives the percentile formula", "⌈p ÷ 100 × n⌉"],
  ["the appendix maps published boundaries onto Callstorm's clocks", "ASR finalization"],
]) {
  const ok = card.includes(needle);
  if (!ok) bad++;
  console.log(`${ok ? "ok  " : "FAIL"}  ${name}`);
}

// A matrix whose answer is fixed by construction: the severe network fails c8
// against the clean baseline, its delay lands in endpointing (+500ms) rather
// than think/speak (+10ms), and the refund node collapses to 2 of 16 there.
const heldNodes = [
  { node: "open", visits: 6, checked: 6, passed: 6, pass_rate: 1 },
  { node: "refund", visits: 6, checked: 6, passed: 6, pass_rate: 1 },
  { node: "close", visits: 6, checked: 0, passed: 0, pass_rate: 0 },
];
const brokeNodes = [
  { node: "open", visits: 16, checked: 16, passed: 16, pass_rate: 1 },
  { node: "refund", visits: 16, checked: 16, passed: 2, pass_rate: 0.125, miss: 'said none of ["refund"]' },
  { node: "close", visits: 0, checked: 0, passed: 0, pass_rate: 0 },
];
const mstep = (name, conc, p95, vs, verdict, ep, ts, nodes) => ({
  name, concurrency: conc, verdict, vs_clean: vs, nodes,
  ttfa: { n: 20, p50_ms: p95 - 5, p90_ms: p95 - 2, p95_ms: p95 },
  endpointing: { n: 20, p50_ms: ep, p95_ms: ep }, think_speak: { n: 20, p50_ms: ts, p95_ms: ts },
});
const matrixRun = {
  steps: [], matrix: { device: "eth0", cohorts: [
    { impairment: { name: "clean" }, tc_command: "tc qdisc del dev eth0 root",
      qdisc: "qdisc noqueue 0: root refcnt 2",
      steps: [mstep("c2", 2, 505, 1, "pass", 200, 300, heldNodes), mstep("c8", 8, 510, 1, "pass", 200, 300, heldNodes)] },
    { impairment: { name: "severe", loss_pct: 5, jitter_ms: 100, delay_ms: 200 },
      tc_command: "tc qdisc replace dev eth0 root netem delay 200ms 100ms loss 5%",
      qdisc: "qdisc netem 8001: root refcnt 2 limit 1000 delay 200ms  100ms loss 5%", breakpoint: "c8",
      steps: [mstep("c2", 2, 900, 1.78, "warn", 700, 310, heldNodes), mstep("c8", 8, 1300, 2.55, "fail", 900, 310, brokeNodes)] },
  ] },
};
const heat = ReactDOMServer.renderToStaticMarkup(React.createElement(mod.ImpairmentHeatmap, { rep: matrixRun }));
for (const [name, needle] of [
  ["heatmap card", "Load against network"],
  ["one row per cohort", ">severe<"],
  ["the exact tc command", "tc qdisc replace dev eth0 root netem delay 200ms 100ms loss 5%"],
  ["the kernel's readback", "qdisc netem 8001"],
  ["a failed cell is marked", "✕"],
  ["breaks-at column", ">c8<"],
  ["offers agent p95 as a cell value", "agent p95"],
  ["names the finding", "The network breaks it before load does"],
  ["says loss arrives as delay over TCP", "not as damaged audio"],
  ["places the delay in endpointing", "The network lands in endpointing"],
]) {
  const ok = heat.includes(needle);
  if (!ok) bad++;
  console.log(`${ok ? "ok  " : "FAIL"}  ${name}`);
}
{
  // Four cells, four fills: a heatmap whose cells render uncoloured is the
  // silent way this one lies.
  const filled = (heat.match(/background:color-mix/g) || []).length;
  const ok = filled === 4;
  if (!ok) bad++;
  console.log(`${ok ? "ok  " : "FAIL"}  ${filled} filled cells for 4 cohort-steps`);
}

const nodes = ReactDOMServer.renderToStaticMarkup(React.createElement(mod.NodeScores, { rep: matrixRun }));
for (const [name, needle] of [
  ["node card", "Which part of the conversation gave way"],
  ["defaults to the worst cohort", "severe · worst"],
  ["names the collapsed node", "refund is the node that gives way"],
  ["quotes its rate", "12.5%"],
  ["an unvisited node is kept", "not reached"],
  ["a node with no assertion shows visits, not a rate", "6 visits"],
]) {
  const ok = nodes.includes(needle);
  if (!ok) bad++;
  console.log(`${ok ? "ok  " : "FAIL"}  ${name}`);
}

const dup = ReactDOMServer.renderToStaticMarkup(React.createElement(mod.IntegrityCard, { integrity: {
  dispatch: { dispatched: 16, received: 16, duplicates: 2, missing: 0, late: 1,
    steps: [{ step: "c8", dispatched: 16, received: 16, duplicates: 2, missing: 0, late: 1 }] } } }));
const lost = ReactDOMServer.renderToStaticMarkup(React.createElement(mod.IntegrityCard, { integrity: {
  dispatch: { dispatched: 16, received: 13, duplicates: 0, missing: 3, late: 0,
    steps: [{ step: "c8", dispatched: 16, received: 13, duplicates: 0, missing: 3, late: 0 }] } } }));
for (const [name, html, needle] of [
  ["integrity card", dup, "Did every call come back exactly once"],
  ["duplicates are named and said to be dropped", dup, "came back twice"],
  ["missing calls are the headline", lost, "3 of 16 calls never came back"],
  ["and point at the fleet first", lost, "Check the workers before the agent"],
]) {
  const ok = html.includes(needle);
  if (!ok) bad++;
  console.log(`${ok ? "ok  " : "FAIL"}  ${name}`);
}

// A real matrix: graph-ref against the in-cluster reference agent under the
// four default networks. Its answers are known. refagent's replies fix the node
// rates in every cohort, and its injected 300ms think/speak means any network
// cost has to land in endpointing.
const realMatrix = JSON.parse(fs.readFileSync("testdata/run-matrix.json", "utf8"));
seeded = 0;
current = realMatrix;
const matrixCard = ReactDOMServer.renderToStaticMarkup(
  React.createElement(mod2.ReportCard, { refs: REFS, id: "run-matrix" }));
for (const [name, needle] of [
  ["heatmap mounts in the full report", "Load against network"],
  ["every default cohort is a row", ">moderate<"],
  ["the recorded command is shown", "tc qdisc replace dev eth0 root netem delay 200ms 100ms loss 5%"],
  ["the real run breaks on the network", "The network breaks it before load does"],
  ["and the delay lands in endpointing", "The network lands in endpointing"],
  ["the refund node is the one that gives way", "refund is the node that gives way"],
  ["an in-process matrix has no pipeline to check", "No pipeline to check"],
]) {
  const ok = matrixCard.includes(needle);
  if (!ok) bad++;
  console.log(`${ok ? "ok  " : "FAIL"}  ${name}`);
}
{
  const cells = realMatrix.matrix.cohorts.reduce((a, c) => a + c.steps.length, 0);
  // Heatmap cells only: the question charts' legends use the same colours, so
  // counting every colour-mix swatch on the page would count them too.
  const filled = (matrixCard.match(/class="hm[^"]*" style="background:color-mix/g) || []).length;
  const ok = filled === cells;
  if (!ok) bad++;
  console.log(`${ok ? "ok  " : "FAIL"}  ${filled} filled cells for ${cells} cohort-steps in the real matrix`);
  // Every cohort in this run scored the same fixed answers, so every cohort
  // must show refund at 0% and number over twice the visits of open.
  const zero = realMatrix.matrix.cohorts.every(c => c.steps.every(s =>
    s.nodes.find(n => n.node === "refund").pass_rate === 0 &&
    s.nodes.find(n => n.node === "number").visits === 2 * s.nodes.find(n => n.node === "open").visits));
  if (!zero) bad++;
  console.log(`${zero ? "ok  " : "FAIL"}  the graph's known answer held in every cohort`);
}

// Agent TTFA: the observed wait with the measured round trip taken out. The
// fixture is arithmetic -- 100ms of round trip on a 640ms turn leaves 540ms, a
// 16% share, so exactly one verdict is right -- and a run where no pong came
// back says so rather than showing a corrected figure it does not have.
const agentHtml = ReactDOMServer.renderToStaticMarkup(React.createElement(mod.AgentLatency, { steps: [
  { name: "c2", concurrency: 2, ttfa: { n: 20, p50_ms: 600, p95_ms: 650 },
    transport_rtt: { n: 20, p50_ms: 100, p95_ms: 110 }, agent_ttfa: { n: 20, p50_ms: 500, p95_ms: 545 } },
  { name: "c8", concurrency: 8, ttfa: { n: 40, p50_ms: 640, p95_ms: 700 },
    transport_rtt: { n: 30, p50_ms: 100, p95_ms: 120 }, agent_ttfa: { n: 30, p50_ms: 540, p95_ms: 590 } },
] }));
const noPong = ReactDOMServer.renderToStaticMarkup(React.createElement(mod.AgentLatency, { steps: [
  { name: "c2", concurrency: 2, ttfa: { n: 10, p50_ms: 600, p95_ms: 650 },
    transport_rtt: { n: 0, p50_ms: 0, p95_ms: 0 }, agent_ttfa: { n: 0, p50_ms: 0, p95_ms: 0 } },
] }));
for (const [name, html, needle] of [
  ["agent card", agentHtml, "The wait, with the network taken out"],
  ["shows the agent's share", agentHtml, "540ms"],
  ["shows how much of the step was corrected", agentHtml, "30 of 40 turns"],
  ["a small network share leaves the wait with the agent", agentHtml, "The wait is the agent"],
  ["says the correction is an estimate", agentHtml, "The correction is an estimate"],
  ["a run whose server answered no pings says so", noPong, "no pong came back"],
]) {
  const ok = html.includes(needle);
  if (!ok) bad++;
  console.log(`${ok ? "ok  " : "FAIL"}  ${name}`);
}
{
  const both = agentHtml.includes("of the wait.</b>");
  if (both) bad++;
  console.log(`${!both ? "ok  " : "FAIL"}  does not also call the network a large share`);
}

// The known answer for agent TTFA, from a real run in kind: the reference agent
// injects 500ms, and netem adds 100ms of delay to the calling pod. Observed TTFA
// has to rise by the delay, and agent TTFA has to stay where the clean cohort
// had it -- otherwise the correction is wrong, whatever the card says.
const rttRun = JSON.parse(fs.readFileSync("testdata/run-rtt.json", "utf8"));
{
  const cohort = name => rttRun.matrix.cohorts.find(c => c.impairment.name === name);
  const clean = cohort("clean"), delay = cohort("delay-100");
  const pairs = delay.steps.map((s, i) => [s, clean.steps[i]]);
  const rose = pairs.every(([s, base]) => s.ttfa.p50_ms - base.ttfa.p50_ms > 90);
  const held = pairs.every(([s, base]) => Math.abs(s.agent_ttfa.p50_ms - base.agent_ttfa.p50_ms) < 10);
  const ok = rose && held;
  if (!ok) bad++;
  console.log(`${ok ? "ok  " : "FAIL"}  100ms of delay raises observed TTFA and leaves agent TTFA where clean had it`);
}
seeded = 0;
current = rttRun;
const rttCard = ReactDOMServer.renderToStaticMarkup(
  React.createElement(mod2.ReportCard, { refs: REFS, id: "run-rtt" }));
{
  const ok = rttCard.includes("The wait, with the network taken out") && rttCard.includes("30 of 30 turns");
  if (!ok) bad++;
  console.log(`${ok ? "ok  " : "FAIL"}  the real run's agent card renders inside the report`);
}

// The questions section, against the fixtures already here. None of them was
// shaped to answer every question, so these assert the answers they can give
// and the reasons they give for the rest: an unanswerable question has to say
// why, and no answer may fail to compute.
for (const [name, html, needle] of [
  ["the questions section heads the report", card, "questions answered, in plain words"],
  ["capacity is asked in plain words", card, "How many calls at the same time can it take"],
  ["a chart is drawn for an answered question", card, "slowest callers&#x27; wait"],
  ["what the test can't tell you is listed", card, "What this kind of test can&#x27;t tell you"],
  ["a run with no rush step says so", card, "no sudden-rush step"],
  ["a rush at a level never built up to says why it can't compare", phasedCard, "never built up to that same level"],
  ["a recovery without timestamps says what is missing", phasedCard, "predates call timestamps"],
  ["the network question is answered from a matrix", matrixCard, "A bad connection breaks it before load does"],
  ["a matrix run's job question uses its scenario checks", matrixCard, "failed its check at every load"],
]) {
  const ok = html.includes(needle);
  if (!ok) bad++;
  console.log(`${ok ? "ok  " : "FAIL"}  ${name}`);
}
{
  const broken = [card, legacy, phasedCard, judgedCard, matrixCard, rttCard].filter(h => h.includes("could not be computed")).length;
  if (broken) bad++;
  console.log(`${!broken ? "ok  " : "FAIL"}  every question computed on every fixture (${broken} reports with a broken answer)`);
}

// The readings are prose with numbers spliced into it, and both ways of
// getting that wrong are silent. htm drops whitespace spanning a newline
// exactly as JSX does, so a value on its own line arrives glued to the word
// before it; and a field read off the wrong object prints NaN in a sentence
// that otherwise looks finished. Neither throws, so neither shows up in any
// check that only asks whether the card rendered.
// Every reading rendered above, scanned together: a glue bug in one card is
// the same bug in all of them, and a check that only looks at the first is how
// the last one ships broken.
// A load test made of several runs. Parts sharing a suite fold into one history
// entry where the newest part sits, with the worst verdict of any part; a run
// with no suite stays its own entry. Built with the real hooks: the suite card
// keeps which part is open in state, and the stub above would seed it a report.
globalThis.React = React;
const modS = new Function(src + "\n;return { groupRuns, SuiteCard };")();
const part = (pid, scenario, started, extra) => Object.assign({
  id: pid, profile: "sweep-deepgram-30", scenario, target: "", started_at: started, duration_s: 600, steps: 5,
  verdict: "pass", peak_concurrency: 40, baseline_p95_ms: 900, worst_p95_ms: 1300, wer_mean: 0,
  harness_degraded: false, suite: "deepgram-full-test",
}, extra);
const history = [
  part("p3", "refund-escalation", "2026-09-13T17:30:00Z", { profile: "impair-ref", cohorts: 4, peak_concurrency: 8, network_breaks: ["moderate at c2", "severe at c2"] }),
  part("solo", "refund-escalation", "2026-09-13T17:20:00Z", { suite: undefined }),
  part("p2", "refund-bargein", "2026-09-13T17:00:00Z", { verdict: "fail", breakpoint: "c40 at 40 concurrent", judged: 20, passed: 15, harness_degraded: true }),
  part("p1", "refund-escalation", "2026-09-13T16:44:00Z", { judged: 20, passed: 18 }),
];
const entries = modS.groupRuns(history);
const suite = entries[0];
const suiteHtml = ReactDOMServer.renderToStaticMarkup(
  React.createElement(modS.SuiteCard, { group: suite, refs: REFS, runs: history }));
for (const [name, ok] of [
  ["suite parts fold into one entry, a run without a suite stays its own", entries.length === 2 && suite.kind === "suite" && entries[1].id === "solo"],
  ["suite parts in the order they ran", suite.parts.map(p => p.id).join() === "p1,p2,p3"],
  ["suite verdict is its worst part's", suite.verdict === "fail"],
  ["suite sums judged calls across parts", suite.judged === 40 && suite.passed === 33],
  ["suite counts its bad-network parts and scenarios", suite.network === 1 && suite.scenarios.length === 2],
  ["suite card names every part", ["load sweep", "bad networks · 4 conditions", "refund-bargein"].every(s => suiteHtml.includes(s))],
  ["suite card names what broke a bad-network part", suiteHtml.includes("held; on bad networks: moderate at c2, severe at c2")],
  ["suite reading counts broken parts", suiteHtml.includes("2 of 3 parts broke somewhere")],
  ["suite reading flags a part the test machine could not keep up with", suiteHtml.includes("Treat refund-bargein as rough")],
  ["suite card opens its first part below the overview", suiteHtml.includes("Part 1 of 3 · load sweep · refund-escalation")],
]) {
  if (!ok) bad++;
  console.log(`${ok ? "ok  " : "FAIL"}  ${name}`);
}

const ALL = [card, legacy, refsHtml, drifted, stuck, nojudge, task, thinHtml, phasedCard, judgedCard, heat, nodes, dup, lost, matrixCard, agentHtml, noPong, rttCard, suiteHtml].join(String.fromCharCode(10));
const prose = [...ALL.matchAll(/<div class="read[^"]*">([\s\S]*?)<\/div>/g)]
  // Inline tags are dropped rather than spaced out: <b> and <code> sit inside
  // sentences, so replacing them with a space would invent gaps the reader
  // never sees and hide the glued ones that matter. The blocks are joined with
  // a gap instead, since those really are separate sentences.
  .map(m => m[1].replace(/<[^>]+>/g, "").replace(/&#x27;/g, "'").replace(/&amp;/g, "&")
                .replace(/\s+/g, " ").trim())
  .join("  |  ");

const broken = {
  "NaN or undefined in a reading": /NaN|undefined/,
  "number glued to the preceding word": /[a-z]{2}\d/,
  "word glued to the preceding number": /\d[a-zA-Z]{3,}/,
  "space before a full stop": / \./,
  "dash glued to what follows": /[—–][0-9A-Za-z]/,
};
for (const [name, re] of Object.entries(broken)) {
  const hit = prose.match(re);
  if (hit) bad++;
  console.log(`${hit ? "FAIL" : "ok  "}  no ${name}${hit ? ` — found ${JSON.stringify(prose.slice(Math.max(0, hit.index - 30), hit.index + 20))}` : ""}`);
}

// A source lint for the same bug, because the output scan above can only catch
// the glue it can recognise. htm discards whitespace that spans a newline
// exactly as JSX does, so a value at the end of one line and the word at the
// start of the next arrive with nothing between them. That renders as
// "705mson a typical turn" or "the job100.0% of the time": no error, no
// warning, and a sentence that still looks finished.
//
// Reading the rendered prose only finds the branches a fixture happens to
// exercise, and the branch that ships broken is always the other one. This
// reads the template source instead, so an unpriced run's empty state is
// checked as closely as the one the fixture produces.
//
// Attributes are exempt: whitespace between `x2=${a}` and `stroke="b"` is not
// prose and does not matter.
const lines = page.split(/\r?\n/);
const glued = [];
for (let i = 0; i < lines.length - 1; i++) {
  const a = lines[i].replace(/\s+$/, ""), b = lines[i + 1].trim();
  if (!a || !b) continue;
  if (/^[a-zA-Z][\w-]*=/.test(b)) continue;       // next line is a tag attribute
  if (a.endsWith("}") && a.includes("${") && /^[a-z(]/.test(b)) {
    glued.push(`${i + 1}: value then "${b.slice(0, 34)}"`);
  } else if (/[a-z,;:)—–]$/.test(a) && b.startsWith("${")) {
    glued.push(`${i + 2}: "...${a.slice(-34)}" then value`);
  } else if (/<\/(b|code|em|span|i)>$/.test(a) && /^[a-z(]/.test(b)) {
    // The same trimming happens at element boundaries, not only at
    // interpolations: a line ending in </code> and the next word arrive with
    // nothing between them. That is where "Lengthen hold_sbefore trusting
    // that" came from, and no amount of reading the fixture would have found
    // it in a branch the fixture never takes.
    glued.push(`${i + 1}: inline tag then "${b.slice(0, 30)}"`);
  } else if (/[a-z,;:)]$/.test(a) && /^<(b|code|em|span|i)[ >]/.test(b)) {
    glued.push(`${i + 2}: "...${a.slice(-30)}" then inline tag`);
  }
}
console.log(`${glued.length === 0 ? "ok  " : "FAIL"}  ${glued.length} template join sites where htm would drop the space`);
for (const g of glued) console.log(`        ${g}`);
bad += glued.length;

console.log(`${warnings.length === 0 ? "ok  " : "FAIL"}  ${warnings.length} React warnings during render`);
bad += warnings.length;

process.exit(bad === 0 ? 0 : 1);
