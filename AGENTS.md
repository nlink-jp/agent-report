# AGENTS.md — agent-report

## What this is

A util-series CLI that reads the session transcripts of gem-agent and lagent
on the user's machine and prints a fixed set of numeric items — a usage
summary a third party can inspect and attach to a report. It sends nothing and
emits no free text from the transcripts. Status: scaffold; the transcript
reading and aggregation (RFP Phase 1) are not implemented yet.

## Build & test

```sh
make build    # → dist/agent-report (signed when a Developer ID cert is present)
make test     # or: go test ./...
make vet
make package  # darwin-arm64 zip + notarization (release only)
make verify-release
```

## Structure

```
main.go          version injection, calls cmd.Execute
cmd/root.go      flag parsing and validation (standard library flag)
docs/{en,ja}/    RFP
scripts/         codesign / notarize / brew — vendored byte-identical
                 from nlink-jp/.github templates; do not edit here
```

## Inputs

- gem-agent: `~/.local/state/gem-agent/sessions/**/*.jsonl`
- lagent: `~/.local/state/lagent/sessions/**/*.jsonl`

Both are JSONL with a `session` header record (`schema` 2) followed by
records of the form `{"ts", "kind", "data"}`. lagent includes this format in
its stability promise (lagent ADR-0023); gem-agent does not state whether it
does, and gem-usage-lens is its other consumer.

## Gotchas

- **darwin/arm64 only** — an exception to the util-series four-target
  standard, because the runtimes are macOS-only (RFP §7).
- **`--version` and `version` print the same line** — `brew test` runs
  `--version`; a test pins both.
- **Exit codes** — 0 success, 1 runtime error, 2 usage error.
- **gem-agent tool-call ids collide within a session** (short Vertex ids) —
  identify a call by file and position, not by id.
- **Launch mode is not recorded** by either runtime (`-p`, `--auto`,
  `--allow`); report it as not recorded.

## Design reference

- RFP: `docs/en/agent-report-rfp.md` (`docs/ja/agent-report-rfp.ja.md`)
