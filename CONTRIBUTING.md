# Contributing to Callstorm

Contributions to measurement accuracy, scenarios, dashboard usability, and
documentation are welcome. For a substantial change, open an issue describing
the problem and proposed behavior before starting implementation.

## Set up

Use Go 1.25 or newer. Node.js and npm are needed for dashboard render checks.
Run commands from the repository root unless shown otherwise.

```sh
go mod download
go test ./...
cd cmd/dashboard/ui
npm install
npm run check
```

Go tests and dashboard fixtures are the starting point for local validation;
running the real-agent CLI is a separate, credentialed operation. See the
[README](README.md#quickstart) for running the application.

## Make a change

1. Create a branch with a focused change.
2. Keep metric calculations and their explanations consistent. Do not pool
   percentiles across load stages or turn missing samples into measured zeroes.
3. Add regression coverage when changing scoring, timing, aggregation, or
   data handling. For layout changes, check desktop and mobile and exercise
   the affected controls.
4. Format changed Go files with `gofmt`. Run the checks relevant to your change.
5. Open a pull request describing the problem, resulting behavior, and validation.

The current Go module path is `github.com/yakshgandhi/callstorm`; retain that
path in imports even though the repository is hosted under `ai-calypse`.

## Report a bug

Include the commit, operating system, command or dashboard steps, expected
behavior, and observed behavior. A small scenario or sanitized report helps
reproduce measurement issues. Include whether test timing was marked suspect.

Do not attach API keys, `.env` files, private transcripts, or customer audio.
Run artifacts can contain complete conversations and agent configuration.
Prefer a synthetic reproduction against the reference agent.

## Documentation and screenshots

Keep the main README focused on getting started. Detailed measurement and
deployment documentation belongs in [the technical guide](docs/technical-guide.md).
README images live in `docs/images/`; capture the real dashboard with synthetic
test data and keep the selected test and its caveats visible.

## License

Contributions are made under the project's [MIT license](LICENSE).
