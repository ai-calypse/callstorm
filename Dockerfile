# Callstorm worker and reference agent in one image.
#
# The caller audio cache is baked in. A fleet that synthesized its own audio
# would pay the text-to-speech bill once per pod and again on every restart, and
# an autoscaler would turn a cost into a surprise. Shipping the audio makes a
# scaled run free of the one thing in Callstorm that costs money per caller.
FROM golang:1.25 AS build

WORKDIR /src

# Dependencies first, so editing source does not re-download the module graph.
COPY go.mod go.sum ./
RUN go mod download

COPY . .

# Static binaries: the runtime image has no libc to link against.
RUN CGO_ENABLED=0 go build -trimpath -o /out/worker ./cmd/worker && \
    CGO_ENABLED=0 go build -trimpath -o /out/refagent ./cmd/refagent && \
    CGO_ENABLED=0 go build -trimpath -o /out/callstorm ./cmd/callstorm && \
    CGO_ENABLED=0 go build -trimpath -o /out/dashboard ./cmd/dashboard

# The impairment matrix image: build with --target impair.
#
# netem is driven by tc, which distroless does not carry, and changing a pod's
# queue discipline needs root with NET_ADMIN, which the worker image refuses on
# purpose. So the one process allowed to degrade a network gets its own image
# rather than every worker being given the means to. It also carries tar, which
# kubectl cp needs to copy the run's artifacts back out.
FROM debian:bookworm-slim AS impair

RUN apt-get update && \
    apt-get install -y --no-install-recommends iproute2 && \
    rm -rf /var/lib/apt/lists/*

COPY --from=build /out/callstorm /callstorm
COPY --from=build /src/scenarios /scenarios
COPY --from=build /src/profiles /profiles
COPY .audiocache /cache

ENTRYPOINT ["/callstorm"]

FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=build /out/worker /worker
COPY --from=build /out/refagent /refagent
COPY --from=build /out/callstorm /callstorm
COPY --from=build /out/dashboard /dashboard

# Scenarios and profiles travel with the image so the dispatcher can run as a
# Job with nothing mounted.
COPY --from=build /src/scenarios /scenarios
COPY --from=build /src/profiles /profiles

# Pre-synthesized caller audio. Keyed by a hash of voice, sample rate and text,
# so a worker finds it without knowing it was put there ahead of time.
COPY --chown=nonroot:nonroot .audiocache /cache

USER nonroot
ENTRYPOINT ["/worker"]
