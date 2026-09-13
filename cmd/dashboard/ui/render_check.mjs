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

const mod = new Function(src + "\n;return { App, ReportCard, ComponentShare, CostChart, GradeScale, PhaseTable, TaskSuccessCard, bandOf, BANDS };")();

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
  React.createElement(mod2.ReportCard, { id }));

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
  React.createElement(mod2.ReportCard, { id }));

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


// The absolute grade. Rendered from the fixture's own steps, then checked
// against the grade Go wrote into the report: two implementations of the same
// three thresholds is exactly the kind of pair that drifts apart silently.
const grade = ReactDOMServer.renderToStaticMarkup(
  React.createElement(mod.GradeScale, { steps: run.steps }));

for (const [name, needle] of [
  ["grade card", "without comparing it to itself"],
  ["names the natural band", "NATURAL"],
  ["explains where the bands come from", "200ms of"],
  ["grade reading present", "read-do"],
  ["band scale is drawn", "<rect"],
  ["each step is placed on it", "<circle"],
]) {
  const ok = grade.includes(needle);
  if (!ok) bad++;
  console.log(`${ok ? "ok  " : "FAIL"}  ${name}`);
}

for (const s of run.steps) {
  if (!s.grade || !s.grade.typical) continue;
  const js = mod.bandOf(s.ttfa.p50_ms);
  const agree = js === s.grade.typical;
  if (!agree) bad++;
  console.log(`${agree ? "ok  " : "FAIL"}  ${s.name}: page grades ${s.ttfa.p50_ms}ms as ${js}, report says ${s.grade.typical}`);
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
// fixture predates the phase model and the absolute grade, so every assertion
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
  React.createElement(mod2.ReportCard, { id: "run-phases" }));

for (const [name, needle] of [
  ["grade card renders inside the report", "without comparing it to itself"],
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

// Two implementations of the same three thresholds, one in Go and one on the
// page. This is the pair that drifts apart silently, so it is checked on every
// step of a run whose report actually carries the Go answer.
let graded = 0;
for (const s of phased.steps) {
  if (!s.grade || !s.grade.typical) continue;
  graded++;
  for (const [which, jsBand, goBand, v] of [
    ["p50", mod.bandOf(s.ttfa.p50_ms), s.grade.typical, s.ttfa.p50_ms],
    ["p95", mod.bandOf(s.ttfa.p95_ms), s.grade.worst, s.ttfa.p95_ms],
  ]) {
    if (jsBand === goBand) continue;
    bad++;
    console.log(`FAIL  ${s.name} ${which} ${v}ms: page says ${jsBand}, report says ${goBand}`);
  }
}
{
  const ok = graded === phased.steps.length;
  if (!ok) bad++;
  console.log(`${ok ? "ok  " : "FAIL"}  grade agreement checked on ${graded} of ${phased.steps.length} steps`);
}

// The known-answer check, restated on the page: the reference agent was given
// a capacity of 6 and 25ms of degradation per caller past it, so 24 concurrent
// must land in the breakdown band and 4 concurrent must not.
{
  const spike = phased.steps.find(s => s.phase && s.phase.kind === "spike");
  const ramp = phased.steps.find(s => s.name === phased.baseline);
  const right = spike && ramp &&
    mod.bandOf(spike.ttfa.p50_ms) === "breakdown" &&
    mod.bandOf(ramp.ttfa.p50_ms) === "acceptable";
  if (!right) bad++;
  console.log(`${right ? "ok  " : "FAIL"}  the injected latency lands in the band it should (${ramp && Math.round(ramp.ttfa.p50_ms)}ms then ${spike && Math.round(spike.ttfa.p50_ms)}ms)`);
}

// The same run with a judge block attached, to prove the task card mounts
// inside the whole page and not only when rendered on its own.
const withJudge = JSON.parse(JSON.stringify(phased));
withJudge.judge = judged;
seeded = 0;
current = withJudge;
const judgedCard = ReactDOMServer.renderToStaticMarkup(
  React.createElement(mod2.ReportCard, { id: "run-judged" }));
for (const [name, needle] of [
  ["task card mounts in the full report", "Did it actually do the job"],
  ["and brings its finding with it", "Mishearing is costing it the task"],
]) {
  const ok = judgedCard.includes(needle);
  if (!ok) bad++;
  console.log(`${ok ? "ok  " : "FAIL"}  ${name}`);
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
const ALL = [card, legacy, grade, drifted, stuck, nojudge, task, thinHtml, phasedCard, judgedCard].join(String.fromCharCode(10));
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
