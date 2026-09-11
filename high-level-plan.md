Here's the full plan. I'll call the project **Callstorm** as a working name (rename later).

## What it does

Callstorm is a load-testing and observability platform for voice AI agents — think k6 or Locust, but the "virtual users" talk. You give it a target (a phone number, a WebSocket endpoint, or a Deepgram Voice Agent config), a set of caller personas and scripts, and a concurrency profile (ramp to 500 callers over 5 minutes, hold, spike). It spins up synthetic callers that speak with Deepgram TTS, listens to the agent with Deepgram STT, and measures everything: how fast the agent responds, whether it handles interruptions, whether it says the right things, and where it falls over under load. Output is a live Grafana dashboard plus a per-run report card.

Three core capabilities:
- Scenario engine: personas (angry customer, elderly caller, non-native speaker, someone who interrupts constantly), scripted turns with branching on what the agent says, and expected outcomes ("agent must offer a refund within 3 turns").
- Load engine: ramp/hold/spike profiles, thousands of concurrent WebSocket audio streams, backpressure so the test harness never becomes the bottleneck.
- Judgement engine: per-turn latency metrics, barge-in handling, transcript accuracy (WER vs. ground truth), dead-air detection, and an LLM judge scoring conversation quality against the scenario's success criteria.

## Skills it showcases

Concurrency and real-time systems (thousands of live audio streams), event-driven architecture (Kafka), observability as a first-class feature (OpenTelemetry traces per call, Prometheus, ClickHouse for analytics), SLO thinking (p50/p95/p99, error budgets), horizontal scaling on Kubernetes, chaos-style failure injection, and applied audio ML evaluation (WER, latency waterfalls, LLM-as-judge). It also shows product taste: this is a tool the voice-AI industry lacks, and it's directly usable by Bland-style companies.

## Technology stack

- Caller workers: Go (goroutines are the right tool for thousands of concurrent WebSockets), Deepgram Aura TTS for the caller's voice, Deepgram Nova/Flux streaming STT for hearing the agent
- Orchestrator/API: Go (or Spring Boot if you want the Java signal — pick based on which JDs you're chasing), Postgres for runs/scenarios/results
- Event bus: Kafka — scenario dispatch, per-turn events, metrics events
- Telemetry: OpenTelemetry → Prometheus (live) + ClickHouse (per-turn analytics, historical comparison)
- Dashboards: Grafana for live runs, React/Next.js app for run history, scenario editor, and report cards
- Judge: an LLM (Claude via API) scoring transcripts against success criteria
- Infra: Docker, Kubernetes (workers as an HPA-scaled deployment), GitHub Actions
- Test targets: Deepgram Voice Agent API as the built-in demo agent, plus your Ringback agent as the "real" target; Twilio SIP trunk as a later phase for phone-number targets

## High-level architectureThe key design decision is the worker: each synthetic caller is one goroutine owning two WebSockets (TTS out to the agent, STT in from the agent), a turn-taking state machine, and a clock that stamps every event — "caller stopped speaking", "agent's first audio byte", "agent stopped speaking", "barge-in attempted", "agent yielded". Those timestamps are the whole product. Everything else is plumbing around them.

## Metrics you'll ship (this is the report card)

- Time to first byte: caller finishes → agent's first audio (p50/p95/p99)
- Turn latency: full response time per turn
- Barge-in yield time: how fast the agent shuts up when interrupted
- Dead air: gaps over 2s with no audio either way
- Transcript accuracy: WER of the agent's speech vs. its own claimed transcript, and of what the agent heard vs. the caller's script
- Task success: LLM judge score against the scenario's success criteria
- Failure rate and concurrent-call ceiling: where p99 crosses your SLO and where calls start dropping
- Cost per call: Deepgram + LLM spend, so a run has a dollar figure

## Diagrams for the posts and README

1. High-level architecture (above) — post #1.
2. Anatomy of one call: a sequence diagram of caller worker ↔ agent with the timestamped events marked, so people see exactly what TTFB and barge-in yield mean. Best explanatory diagram in the project.
3. Latency waterfall for a single turn: STT partials → LLM → TTS first chunk → playout, with real measured bars. Great post on its own.
4. Load profile vs. p99: a chart with concurrency on one axis and p99 latency plus error rate on the other, showing the exact point the target agent degrades. This is the money graph.
5. Worker scaling on Kubernetes: HPA scaling workers on active-call count, with a Kafka consumer-lag graph beside it.
6. Chaos run: kill a worker pod mid-run; show calls rebalancing and the metrics gap you closed (or honestly didn't).
7. Report card mock: the one-page output a voice-AI team would actually read.

## Phases

- Phase 1 (weekend one): one Go worker, one hardcoded scenario, target = Deepgram Voice Agent demo. Emit TTFB and turn latency to stdout. If this works, the project works.
- Phase 2: Kafka dispatch, N workers, ramp profiles, Prometheus + Grafana. First LinkedIn post: architecture plus the concurrency-vs-p99 graph at 100 callers.
- Phase 3: personas, branching scripts, barge-in tests, LLM judge, ClickHouse for run history. Second post: "I interrupted a voice agent 500 times — here's what broke."
- Phase 4: Kubernetes, HPA, chaos run, React dashboard with report cards. Third post: the chaos story.
- Phase 5 (stretch): Twilio SIP so you can point it at any real phone number, and run it against Ringback or a public demo line.

One caution on credits: streaming STT on the agent's side at 500 concurrent calls burns fast. Build a per-run budget cap and a "dry" mode that uses pre-synthesized caller audio cached from earlier runs, so scaling tests reuse audio instead of re-synthesizing it. That cap is itself a good engineering-maturity detail for the writeup.

Want me to write the Phase 1 spec next — the exact worker state machine, event schema, and Deepgram API calls?