# AGENTS.md — agent-report

## What this is

A util-series CLI that reads the session transcripts of gem-agent and lagent
on the user's machine and prints a fixed set of numeric items — a usage
summary a third party can inspect and attach to a report. It sends nothing and
emits no free text from the transcripts.

## Build & test

```sh
make build    # → dist/agent-report (signed when a Developer ID cert is present)
make test     # or: go test ./...
make vet
make vuln     # govulncheck
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
  that matches `versionRe` / `modelRe` / `serverRe`; else `other`. The
  patterns are deliberately narrow (a version is a tag or `git describe`
  output; a model id has at most one `/` and no `@`) — a character-set
  pattern let GCP resource paths and e-mail addresses through. A model id
  the user chose is still shown as written; that residue is accepted and the
  README tells the user to read the `models` section before sending. A new
  report item that needs a string must fit one of these. The leak test in
  `cmd/root_test.go` plants a marker in every free-text field of a gem-agent
  and a lagent transcript, including values a looser pattern would pass —
  extend it when a new field is read. Mutating `modelRe` back to a
  character-set pattern, or passing an unknown lane through, makes it fail.
- **New runtime record kinds** show up as `anomalies.unknown_record_kinds`;
  add the kind to `knownKinds` (and count it if it matters).
- **New built-in tools** in either runtime show up as
  `tools.unknown_name_calls`; add the name to `BuiltinTools`. Models also
  invent names (`default_api:write_file`, `mcp__web-fetch` with no tool) —
  those stay unknown.
- **Elapsed seconds**: both runtimes run a response's calls sequentially and
  write results in call order, so results are matched to calls in order and
  a call's time runs from the previous result (from the response for the
  first call). It includes approval waits; `ask_user` can show hours.
- **Run seconds and operator waits** rely on the order the runtimes write
  today: `auto_decision` and `gate_decision` (and `hook_denied`,
  `tool_skipped`, `tool_excluded`, `tool_not_advertised`) come after the
  response and before the result, and name the first call still awaiting its
  result. Run starts at the call's last decision; a denied or stopped call has
  none. A decision naming any other call is counted as
  `anomalies.unmatched_decisions` and changes nothing — if that count rises,
  the order changed and the run times are no longer trustworthy.
  `ceiling_refused` also marks a call as not run. Refusals that leave no
  record — gem-agent's loop-guard skip, an interruption before execution,
  invalid arguments, a call to an unknown name — enter `run_sec` at almost
  zero; that residue is accepted. One-shot runs deny at the gate without
  asking and record `source: operator`, so `operator_wait_sec` holds
  near-zero samples from them (launch mode is not recorded).
- **Undeclared shell lanes**: both runtimes' `ParseLane` reads a missing or
  empty `access` as the read lane — lagent always, gem-agent since v0.69.0
  (ADR-0073). `laneDefaultRead` uses the header version; a version that is
  not a release tag cannot be placed and is not taken as read.
- **Call kinds** (`multi_call_by_kind`): `readOnlyTools` mirrors the
  built-ins whose `Mutating` flag is false in both runtimes, minus
  `ask_user` (it waits on the operator). MCP tools, `web_fetch` and
  `web_search` are Mutating in the runtimes (egress) and get their own kind.
  When a runtime adds a built-in, decide its kind from that flag.
- **The period**: records before `--since` are still walked, uncounted, so a
  call before the cutoff pairs with its result after it. Report times are UTC.
- **Records written before gem-agent ADR-0057** carry no `source`; they are
  counted under `unrecorded` and treated as main-loop calls for context size.
- **Turns**: a user-role message starts one, except the runtime's own
  injected messages, matched by prefix in `runtimeNotePrefixes`: the `!`
  shell escape, gem-agent's resume note, lagent's "Runtime facts for this
  session:" (written at start, resume and /clear), and both runtimes'
  "note from <runtime>:" late-return notes. A new injected message in either
  runtime inflates turns until its prefix is added.
- **Lenient decoding**: message content and tool arguments are decoded as raw
  JSON; a model writing a non-string `access` must not lose the message.
- **/clear is not countable**: gem-agent's normal /clear opens a new file and
  writes no record, so the report lists it under `not_recorded`.
- **`mode_start` is written once per process**, so a resumed session has
  several; modes count once per session.
- **A missing `--sessions-dir` is an error** (exit 1, path not echoed); a
  missing default location is `missing`.

## Design reference

- RFP: `docs/en/agent-report-rfp.md` (`docs/ja/agent-report-rfp.ja.md`)
