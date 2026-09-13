// Prints the questions section for one run as plain text, beside the raw
// numbers each answer is computed from, so the wording can be checked against
// the data rather than trusted.
//
//   node answers_check.mjs ../../../runs/<run-id>.json [runs.json]
//
// The optional runs.json is the history index (the dashboard's /api/runs.json),
// which the "would we get the same answer again" question needs.
import fs from "node:fs";
import path from "node:path";
import React from "react";
import ReactDOMServer from "react-dom/server";
import htm from "htm";

const RUN = process.argv[2];
if (!RUN) {
  console.error("usage: node answers_check.mjs <run .json> [runs.json]");
  process.exit(2);
}
const rep = JSON.parse(fs.readFileSync(RUN, "utf8"));
const runs = process.argv[3] ? JSON.parse(fs.readFileSync(process.argv[3], "utf8")) : [];
const id = path.basename(RUN, ".json");

const page = fs.readFileSync("index.html", "utf8");
const src = [...page.matchAll(/<script>([\s\S]*?)<\/script>/g)].map(m => m[1]).join("\n");
globalThis.window = { addEventListener: () => {} };
globalThis.document = { getElementById: () => ({}) };
globalThis.htm = htm;
globalThis.React = React;
globalThis.ReactDOM = { createRoot: () => ({ render() {} }) };
globalThis.fetch = async () => ({ ok: true, json: async () => ({}) });

const mod = new Function(src + "\n;return { buildQuestions, ladderOf };")();
const text = node => ReactDOMServer.renderToStaticMarkup(React.createElement("div", null,
  ...[].concat(node).map((c, i) => React.createElement(React.Fragment, { key: i }, c))))
  .replace(/<[^>]+>/g, "").replace(/&#x27;/g, "'").replace(/&amp;/g, "&").replace(/&quot;/g, '"').replace(/\s+/g, " ").trim();

for (const q of mod.buildQuestions(rep, runs, id)) {
  console.log(`\n## ${q.ask}`);
  if (q.missing) {
    console.log(`   NOT ANSWERED: ${q.missing}`);
    continue;
  }
  console.log(`   ${text(q.answer)}`);
  console.log(`   chart: ${q.chart ? "yes" : "none"}`);
}

console.log("\n--- raw numbers");
for (const s of rep.steps) {
  console.log(`${s.name.padEnd(11)} ${String(s.phase.kind).padEnd(8)} c${String(s.concurrency).padEnd(3)} p50 ${s.ttfa.p50_ms} p95 ${s.ttfa.p95_ms} | endpointing p50 ${s.endpointing.p50_ms} think p50 ${s.think_speak.p50_ms} rtt p50 ${s.transport_rtt ? s.transport_rtt.p50_ms : "-"} | verdict ${s.verdict} | timeline ${(s.timeline || []).length} calls | drift ${s.phase.drift} ${s.phase.drift_pct} | back_to_normal ${s.phase.back_to_normal || false} after ${s.phase.back_to_normal_after_s || 0}s | harness ${s.worst_harness_drift_ms}ms`);
}
console.log("ladder:", mod.ladderOf(rep.steps).map(s => `${s.name}@${s.concurrency}`).join(", "));
console.log("hashes:", rep.scenario_hash ? rep.scenario_hash.slice(0, 12) : "none", rep.profile_hash ? rep.profile_hash.slice(0, 12) : "none");
