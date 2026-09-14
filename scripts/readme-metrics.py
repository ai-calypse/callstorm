"""Rewrite the README's latest-measurements table from the newest real-agent run.

Reads every report under runs/, picks the most recent one whose target is not
the local reference agent, and replaces the block between the
<!-- metrics:start --> and <!-- metrics:end --> markers in README.md.
No dependencies beyond the standard library. Exit code 0 whether or not the
README changed; pass --check to exit 1 when it is out of date instead.
"""
import glob
import json
import os
import sys
from datetime import datetime, timezone
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
START, END = "<!-- metrics:start -->", "<!-- metrics:end -->"
LOCAL = ("localhost", "127.0.0.1", "refagent")


def reports():
    for path in glob.glob(str(ROOT / "runs" / "**" / "*.json"), recursive=True):
        name = os.path.basename(path)
        if name.endswith(("-judgements.json", "-insights.json")):
            continue
        try:
            with open(path, encoding="utf-8") as f:
                rep = json.load(f)
        except (OSError, ValueError):
            continue
        if not isinstance(rep, dict) or not rep.get("steps") or not rep.get("started_at"):
            continue
        target = rep.get("target") or ""
        if any(part in target for part in LOCAL):
            continue
        yield path, rep


def ms(stat, key="p95_ms"):
    if not stat or not stat.get("n"):
        return "n/a"
    value = stat.get(key)
    return f"{value / 1000:.2f} s" if value >= 1000 else f"{value:.0f} ms"


def pct(part, whole):
    return "n/a" if not whole else f"{100 * part / whole:.1f}%"


def cell(step, fn):
    try:
        return fn(step)
    except (KeyError, TypeError, ZeroDivisionError):
        return "n/a"


def table(path, rep):
    steps = rep["steps"]
    base = next((s for s in steps if s["name"] == rep.get("baseline")), steps[0])
    peak = max(steps, key=lambda s: (s.get("concurrency", 0), s.get("calls_attempted", 0)))
    started = datetime.fromisoformat(rep["started_at"].replace("Z", "+00:00")).astimezone(timezone.utc)
    calls = sum(s.get("calls_attempted", 0) for s in steps)
    connected = sum(s.get("calls_connected", 0) for s in steps)
    turns = sum(s.get("turns_total", 0) for s in steps)
    judge = rep.get("judge") or {}
    rel = os.path.relpath(path, ROOT).replace(os.sep, "/")
    run_id = os.path.basename(path)[:-5]

    rows = [
        ("Time to first audio, p50", lambda s: ms(s["ttfa"], "p50_ms")),
        ("Time to first audio, p95", lambda s: ms(s["ttfa"])),
        ("Time to first audio, p99", lambda s: ms(s["ttfa"], "p99_ms")),
        ("Agent-reported TTFA, p95", lambda s: ms(s["agent_ttfa"])),
        ("Endpointing, p50", lambda s: ms(s["endpointing"], "p50_ms")),
        ("Endpointing, p95", lambda s: ms(s["endpointing"])),
        ("Think + speak, p95", lambda s: ms(s["think_speak"])),
        ("Turn latency, p95", lambda s: ms(s["turn_latency"])),
        ("Transport round trip, p50", lambda s: ms(s["transport_rtt"], "p50_ms")),
        ("Replies past 800 ms", lambda s: pct(s["waits"]["turns"] - s["waits"]["up_to_800ms"], s["waits"]["turns"])),
        ("Word error rate, mean", lambda s: f"{100 * s['wer']['mean']:.1f}%"),
        ("Interruptions", lambda s: f"{s['conversation']['interruptions']} ({100 * s['conversation']['interruption_rate']:.1f}% of turns)"),
        ("Dead-air turns", lambda s: str(s["conversation"]["dead_air_turns"])),
        ("Calls connected", lambda s: f"{s['calls_connected']} / {s['calls_attempted']}"),
        ("Failed turns", lambda s: f"{s['turns_failed']} / {s['turns_total']}"),
        ("Worst harness drift", lambda s: f"{s['worst_harness_drift_ms']:.0f} ms" + (" (flagged)" if s.get("harness_degraded") else "")),
        ("Agent minutes billed", lambda s: f"{s['cost']['agent_minutes']:.1f}"),
        ("Verdict against baseline", lambda s: f"{s.get('verdict', 'n/a')} (p95 ratio {s.get('p95_ratio', 'n/a')})"),
    ]

    out = [
        f"**Latest run against a real agent:** [`{run_id}`]({rel}), scenario `{rep.get('scenario')}`, "
        f"profile `{rep.get('profile')}`"
        + (f", suite `{rep['suite']}`" if rep.get("suite") else "")
        + f". Started {started:%Y-%m-%d %H:%M} UTC, {calls} calls, {connected} connected, {turns} turns"
        + (f", {judge.get('passed')} of {judge.get('judged')} judged calls completed the task ({judge.get('backend')})" if judge.get("judged") else "")
        + ".",
        "",
        f"| Metric | Baseline: `{base['name']}`, {base.get('concurrency')} at once | Peak: `{peak['name']}`, {peak.get('concurrency')} at once |",
        "| --- | ---: | ---: |",
    ]
    for label, fn in rows:
        out.append(f"| {label} | {cell(base, fn)} | {cell(peak, fn)} |")
    out += [
        "",
        "Time to first audio is measured from the end of caller speech to the first audible agent byte. "
        "Agent-reported TTFA starts from the agent's own end-of-speech event instead. "
        "Definitions and the published lines they are compared with are in the [technical guide](docs/technical-guide.md). "
        "This table is regenerated by `scripts/readme-metrics.py` from the newest report under `runs/`.",
    ]
    return "\n".join(out)


def main():
    check = "--check" in sys.argv
    found = list(reports())
    if not found:
        sys.exit("no real-agent reports under runs/")
    path, rep = max(found, key=lambda pr: pr[1]["started_at"])
    readme = ROOT / "README.md"
    text = readme.read_text(encoding="utf-8")
    if START not in text or END not in text:
        sys.exit("README.md has no metrics markers")
    head, rest = text.split(START, 1)
    _, tail = rest.split(END, 1)
    updated = f"{head}{START}\n{table(path, rep)}\n{END}{tail}"
    if updated == text:
        print("README metrics already current:", os.path.basename(path))
        return
    if check:
        sys.exit("README metrics are out of date; run scripts/readme-metrics.py")
    readme.write_text(updated, encoding="utf-8")
    print("README metrics updated from", os.path.basename(path))


if __name__ == "__main__":
    main()
