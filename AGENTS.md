# AGENTS.md — agent-report

## What this is

A util-series CLI that reads the session transcripts of gem-agent and lagent
on the user's machine and prints a fixed set of numeric items — a usage
summary a third party can inspect and attach to a report. It sends nothing and
emits no free text from the transcripts. Status: RFP Phase 1 implemented,
not yet released.

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
main.go                       version injection, calls cmd.Execute
cmd/root.go                   flags, default transcript locations, wiring
internal/transcript/          JSONL reading (read only; no line limit)
internal/aggregate/report.go  the report's shape — field order is print order
internal/aggregate/aggregate.go  counting; the closed sets live here
internal/render/              JSON, and the table drawn by walking that JSON
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
- **Closed sets, not redaction.** A string from a transcript reaches the
  report only as an enumerated value, a name on `BuiltinTools`, or a name
  that matches `versionRe` / `modelRe` / `serverRe`; else `other`. A new
  report item that needs a string must fit one of these. The leak test in
  `cmd/root_test.go` plants a marker in every free-text field — extend it
  when a new field is read.
- **New runtime record kinds** show up as `anomalies.unknown_record_kinds`;
  add the kind to `knownKinds` (and count it if it matters).
- **New built-in tools** in either runtime show up as
  `tools.unknown_name_calls`; add the name to `BuiltinTools`. Models also
  invent names (`default_api:write_file`, `mcp__web-fetch` with no tool) —
  those stay unknown.
- **Elapsed seconds** run from the assistant message that asked for the call
  to its result, results matched to calls in order. They include approval
  waits; `ask_user` can show hours.
- **Records written before gem-agent ADR-0057** carry no `source`; they are
  counted under `unrecorded` and treated as main-loop calls for context size.
- **Turns**: a user-role message starts one, except the runtime's own
  injected notes (`!` shell escape, resume note).

## Design reference

- RFP: `docs/en/agent-report-rfp.md` (`docs/ja/agent-report-rfp.ja.md`)
