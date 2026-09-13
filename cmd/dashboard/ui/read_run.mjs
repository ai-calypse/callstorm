// Prints the readings the dashboard would show for a given run, as plain text.
//
// The render check asserts that phrases are present; this is for reading the
// sentences. Every silent bug this page has shipped -- a field taken off the
// wrong object printing NaN mid-sentence, a value glued to the word before it
// -- was invisible to "did it render" and obvious the moment someone read the
// output. So this makes reading the output cheap.
//
//   node read_run.mjs ../../../runs/<run-id>.json
import fs from "node:fs";
import React from "react";
import ReactDOMServer from "react-dom/server";
import htm from "htm";

const RUN = process.argv[2];
if (!RUN) {
  console.error("usage: node read_run.mjs <path to a run .json>");
  process.exit(2);
}

const page = fs.readFileSync("index.html", "utf8");
const src = [...page.matchAll(/<script>([\s\S]*?)<\/script>/g)].map(m => m[1]).join("\n");
const run = JSON.parse(fs.readFileSync(RUN, "utf8"));

globalThis.window = { addEventListener: () => {} };
globalThis.document = { getElementById: () => ({}) };
globalThis.htm = htm;
globalThis.ReactDOM = { createRoot: () => ({ render() {} }) };
globalThis.fetch = async () => ({ ok: true, json: async () => run });

let seeded = 0;
const stub = Object.create(React);
stub.useState = () => [seeded++ === 0 ? run : null, () => {}];
stub.useEffect = () => {};
globalThis.React = stub;

const mod = new Function(src + "\n;return { ReportCard };")();
const out = ReactDOMServer.renderToStaticMarkup(
  React.createElement(mod.ReportCard, { id: "read" }));

// Cards are separated by their headings so the readings can be attributed to
// the chart they belong to.
const text = out
  .replace(/<h2[^>]*>/g, "\n\n=== ")
  .replace(/<\/h2>/g, " ===\n")
  .replace(/<div class="read-verdict">/g, "\n  VERDICT: ")
  .replace(/<div class="read-why">/g, "\n  WHY:     ")
  .replace(/<div class="read-do">/g, "\n  DO:      ")
  .replace(/<div class="note">/g, "\n  NOTE:    ")
  .replace(/<[^>]+>/g, "")
  .replace(/&#x27;/g, "'").replace(/&amp;/g, "&").replace(/&quot;/g, '"')
  .replace(/&#x2F;/g, "/").replace(/&lt;/g, "<").replace(/&gt;/g, ">")
  .split("\n")
  .map(l => l.replace(/\s+/g, " ").trimEnd())
  .filter((l, i, a) => l.trim() !== "" || (a[i - 1] || "").trim() !== "")
  .join("\n");

console.log(text);
