# Callstorm

Load testing and observability for voice AI agents. k6 for agents that talk.

Callstorm places synthetic calls against a voice agent, speaks to it with real
TTS, listens to what it says back, and stamps every instant on both sides of the
conversation. It answers one question: **at what concurrency does this agent
stop meeting its own baseline?**

**Status: Phase 2.** Single calls, concurrency profiles, a calibrated reference
target, live Prometheus metrics, and per-turn events over Kafka. Kubernetes,
personas, WER and the LLM judge are not built yet.

## Quickstart

```bash
go build -o bin/callstorm ./cmd/callstorm
go build -o bin/refagent  ./cmd/refagent

# one call against a local reference agent, no API spend
./bin/refagent -ttfa 800ms -endpointing 300ms &
./bin/callstorm -target ws://localhost:8080/v1/agent/converse
```

Credentials come from `.env` (`DEEPGRAM_API_KEY=...`), which **overrides** the
ambient environment. That direction is deliberate: a stale variable inherited
from some other tool is how you end up billing an account you forgot you had.

Against a real Deepgram Voice Agent, drop `-target`. Caller audio is synthesized
once and cached on disk, so re-running a scenario costs nothing after the first
time; only the agent side is billed.

## The load sweep

```bash
./bin/callstorm -profile profiles/sweep-local.json \
    -target ws://localhost:8080/v1/agent/converse -max-concurrency 100
```

```
step         conc   setup   ttfa p50    ttfa p95    ttfa p99    vs base   verdict
baseline     1      100%    502ms       504ms       504ms       1.00x     pass   <- baseline
c5           5      100%    503ms       505ms       506ms       1.00x     pass
c10          10     100%    503ms       510ms       511ms       1.01x     pass
c25          25     100%    877ms       879ms       880ms       1.74x     warn
c50          50     100%    1502ms      1505ms      1506ms      2.99x     fail

BREAKPOINT   c50 at 50 concurrent: p95 TTFA 1505ms is 2.99x baseline
harness      73ms worst-case pacing drift across the run
```

Each run writes a JSON report, a CSV, and an SVG of the curve.

**Steps are scored against the baseline step, not an absolute latency target.**
p95 within 1.5x passes, 1.5-2x warns, past 2x fails; call setup below 97% fails
outright. A threshold that is generous for one agent is unreachable for another,
so the run reports degradation rather than a number someone else picked.

**Percentiles are bucketed per step and never pooled across the ramp.** A p95
averaged over a rising ramp mixes the easy start with the hard finish and hides
exactly the degradation the run exists to find.

## Why there is a reference agent

`cmd/refagent` speaks the Deepgram Voice Agent wire protocol but has no STT, no
LLM and no TTS. It waits a configured number of milliseconds and emits a tone.

That makes it the calibration instrument. Injected latency is known exactly, so
a measured TTFA that disagrees means Callstorm is wrong and nothing it reports
about a real agent can be trusted:

| injected | measured | at |
|---|---|---|
| TTFA 800ms | 802-808ms | 1 caller |
| endpointing 300ms | 300-302ms | 1 caller |
| 500ms + 25ms per caller past 10 (predicts 875ms) | 878ms | 25 callers |
| same (predicts 1500ms) | 1503ms | 50 callers |

It is also a load target with no bill and no rate limit, which matters because
Deepgram caps Voice Agent connections at **45 concurrent on pay-as-you-go**. The
preflight refuses a profile that would exceed it rather than let connection
refusals get reported as agent failures.

## What it measures, and why it is split up

The headline number is **time to first audio**: the caller stopped talking, and
this is how long the line stayed dead before the agent's first audio arrived.

```
caller stops talking
   |
   |<--- endpointing --->|<--- think + speak --->|
   |                     |                       |
   |            agent decides the         agent's first
   |            turn is over              audio byte
   |
   |<------------------ TTFA ------------------->|
```

**Endpointing** is turn-detection tuning. **Think + speak** is model and TTS
choice. Different teams' problems, and against a real agent they behave
completely differently:

| | spread across 11 turns, 3 runs |
|---|---|
| endpointing | 239-319ms (80ms) |
| think + speak | 439-2088ms (4.8x) |

Endpointing is metronome-stable. All the tail latency lives in think + speak.

Published guidance defines voice latency from *end of utterance **detected***,
which leaves endpointing outside the breakdown. Callstorm starts from when the
caller actually stopped, because it generated the audio and knows the ground
truth. That is the payoff of driving the caller synthetically.

Also reported: **`heard`**, what the agent's STT actually transcribed. When it
diverges from what the caller said, the agent answered a different question -- a
failure no latency metric catches.

## How the measurement stays honest

**Caller audio is paced at realtime, and never bursts to catch up.** Endpointing
runs on audio duration, not on how fast bytes arrive, so pushing PCM at the
socket as fast as it is accepted measures nothing. Frames are scheduled against
absolute deadlines; when the pump falls behind under load it re-anchors rather
than replaying missed deadlines back to back, because catching up would push
audio out *faster* than realtime and corrupt the agent's turn detection.

**Every run reports its own pacing drift.** ~20ms at low concurrency. If a step
exceeds 100ms it is flagged: at that point this machine, not the agent, is the
limit on the test. A load generator that cannot show it was not the bottleneck
is not measuring anything.

**The line never goes silent.** When the caller has nothing to say the pump
sends silence, like an open phone line. Turn detection needs to hear it.

**The agent finishes speaking before the caller replies.** Deepgram streams TTS
about 1.5x faster than realtime, so `AgentAudioDone` is when audio stopped
*arriving*, not when the agent stopped *talking*. Callstorm tracks playout and
waits for it.

**The caller yields when talked over.** If the agent starts speaking mid-
utterance the caller stops, as a person would. Carrying on spills the rest of
the utterance into the next turn and silently corrupts it.

**Negatives are kept, never clamped.** A negative TTFA means the agent started
talking before the caller finished -- the single most interesting thing a run
can find.

## Live metrics

```bash
./bin/callstorm -profile profiles/sweep-local.json -metrics :9464 ...
docker compose -f deploy/docker-compose.yml up -d   # Prometheus + Grafana
```

Histograms for TTFA, endpointing, think/speak, turn latency and harness drift,
all labelled by step; a gauge for active calls; counters for turn and call
outcomes. Grafana opens straight onto the provisioned dashboard at
`localhost:3000`.

**Turns are recorded as they finish, not when their call ends.** Under load
every call in a step completes at roughly the same moment, so reporting at call
end would deliver a step's measurements in one burst after the step was already
over -- useless for watching an agent buckle.

**`/metrics` is held open for `-metrics-linger` (default 5s) after the run.**
A step's turns land in the registry as that step ends, and the breakpoint is by
definition the *last* step. Exiting the instant the run finished dropped
exactly the numbers the run exists to produce, and left `active_calls` frozen
at its last non-zero value instead of back at rest.

**The stack needs Prometheus 3.8 or newer**, where native histograms became a
scrape-config setting rather than an experimental command-line flag. Each
latency histogram is exposed twice: native buckets grow by a fixed ratio and
resolve a quantile to about 1% of its own value, while classic fixed buckets
can only place it somewhere inside the bucket it fell in. On one run whose true
p95 was 504ms, the classic buckets drew it at 738ms -- and reported two
different concurrency steps as identical, because both fell in the same bucket.
The percentile panels read the native series; the distribution heatmap reads
the classic ones, which is what a heatmap needs.

Percentiles on the dashboard are for watching a run happen. The exact figures
come from the run report, which sorts the real durations.

## Per-turn events over Kafka

```bash
./bin/fakekafka &                                    # in-process broker, no Docker
./bin/collector -kafka localhost:9092 &
./bin/callstorm -profile ... -kafka localhost:9092
```

The volume is tiny -- a few thousand events per run -- so Kafka is not here for
throughput. It is here so workers and the metrics sink scale independently, and
so **consumer lag is the backpressure signal**: if the collector keeps up while
callers are placing calls, the measurement pipeline had headroom.

Produces are fire-and-forget. A caller goroutine is holding a live socket on a
realtime audio deadline; blocking it for a broker ack would delay the next frame
and corrupt the measurement the event is reporting.

`cmd/fakekafka` runs a real Kafka protocol implementation in-process, so the
pipeline is runnable and testable without Docker or a JVM.

## A real finding, already

On one run against Deepgram the agent endpointed after a 300ms pause following
the word "No", started replying 4.8 seconds before the caller had finished, and
was truncated to 520ms of audio when the caller's continued speech barged in.
The reply -- "I understand you might not want a refund" -- was the opposite of
what was asked, because it had only heard "No."

```
turn  ttfa       endpointing   think+speak   agent spoke
2     -4809ms    -5254ms       444ms         520ms       agent talked over the caller
```

Same scenario, byte-identical caller audio, two other runs: no barge-in at all.
Identical input, divergent turn detection. That is the argument for running a
scenario many times rather than once, and it is why the caller's audio is cached
and reused rather than regenerated.

## Layout

```
cmd/callstorm        CLI, report card, sweep
cmd/refagent         calibrated reference target
cmd/collector        consumes turn events, reports consumer lag
cmd/fakekafka        in-process Kafka broker for local runs
internal/worker      one caller: websocket, turn state machine, audio pump
internal/loadgen     concurrency profiles, per-step aggregation, verdicts
internal/metrics     the clock: event log and metric derivation
internal/audio       frame math, playout, call recorder
internal/chart       sweep SVG
internal/telemetry   Prometheus
internal/bus         Kafka producer, consumer, lag
internal/tts         Aura synthesis with a shared cache
internal/scenario    scenario schema
```

One caller is one goroutine owning a socket, a state machine and a clock.
Everything else is plumbing around the timestamps.

## Roadmap

- **Phase 3** - personas, branching scripts, deliberate barge-in tests, WER
  against the caller's ground-truth script, LLM judge, ClickHouse for history.
- **Phase 4** - Kubernetes, HPA on active-call count, chaos runs, React
  dashboard.
- **Phase 5** - Twilio SIP, so it can dial a real phone number.
