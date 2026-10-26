# Maintenance and delivery

## Pull-request checks

The workflow files in [`.github/workflows`](../.github/workflows/) are the source of truth. Required checks
cover the relevant build/tests, repository documentation links, workflow syntax, dependency review, and
CodeQL analysis. CodeQL covers Actions plus Go;
language analysis complements runtime tests rather than replacing them.

`main` uses pull requests, linear history, resolved review threads, and passing checks. Force pushes and branch
deletion are blocked. No approving-review count is required for the solo maintainer. Dependabot groups compatible
updates weekly; review them as normal code changes. Third-party Actions use immutable commit pins.

The test-only `github.com/twmb/franz-go/pkg/kfake` module uses a pseudo-version. Dependabot currently
tries parent-module release tags that do not contain that package, so version updates for this one module
are excluded. Review its upstream revisions manually, update with `go get` at an explicit revision,
and rerun the Go race suite. Dependency review and security analysis remain active.

## Release procedure

1. Merge the release changes through a green PR and inspect the main-branch checks.
2. Choose a new semantic version and create a `vMAJOR.MINOR.PATCH` tag on that commit.
3. Push the tag. The release workflow will build Linux/macOS amd64/arm64 CLI archives and SHA-256 checksums, then create a draft GitHub release. Pages delivery is automatic only after successful main-branch CI, using that run's dashboard artifact.
4. Inspect the draft, provenance/checksums or image digests, documentation, and known limitations before publishing.

A draft is not a published release. Publishing is a deliberate maintainer step. Do not reuse an existing version
tag. Failed release jobs must be investigated before retrying; no step suppresses a failed test.

## Evidence and demos

The [demo guide](demo.md) states what was recorded and what it proves. Keep capture inputs free of personal
accounts and credentials. Historical results retain their original conditions and dates. A green build alone
is not evidence that every test layer ran or that the software is production-ready.

## Operational follow-up

Check failing main builds promptly, triage dependency/security reports, and update setup instructions when
runtime requirements change. [Security policy](../SECURITY.md).
