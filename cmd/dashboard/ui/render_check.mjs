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

const mod = new Function(src + "\n;return { App, ReportCard, ComponentShare, CostChart };")();

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


// The readings are prose with numbers spliced into it, and both ways of
// getting that wrong are silent. htm drops whitespace spanning a newline
// exactly as JSX does, so a value on its own line arrives glued to the word
// before it; and a field read off the wrong object prints NaN in a sentence
// that otherwise looks finished. Neither throws, so neither shows up in any
// check that only asks whether the card rendered.
const prose = [...card.matchAll(/<div class="read[^"]*">([\s\S]*?)<\/div>/g)]
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
};
for (const [name, re] of Object.entries(broken)) {
  const hit = prose.match(re);
  if (hit) bad++;
  console.log(`${hit ? "FAIL" : "ok  "}  no ${name}${hit ? ` — found ${JSON.stringify(prose.slice(Math.max(0, hit.index - 30), hit.index + 20))}` : ""}`);
}

console.log(`${warnings.length === 0 ? "ok  " : "FAIL"}  ${warnings.length} React warnings during render`);
bad += warnings.length;

process.exit(bad === 0 ? 0 : 1);
