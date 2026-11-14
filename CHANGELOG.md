# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/).

## [Unreleased]

### Changed

- `make package` runs `make vuln` (`govulncheck ./...`) first and stops on a
  finding, so a release cannot be built past one. The target existed; nothing
  ran it on the way to a release.

## [0.3.0] - 2026-11-14

### Added

- `main_calls`: the main loop's model calls taken one at a time — the
  seconds each kept the operator waiting, the seconds lost to retries, and
  tables of calls, prompt and cached tokens, cache hits and waits: by prompt
  size, after whatever reset the prompt (session start, compaction, resume,
  clear, a change of tools or system prompt), and by how long ago the call
  before ended (`by_idle`) and started (`by_prefix_age`). It reads records
  the runtimes have always written, so it works on transcripts that already
  exist.
- `tokens_by_model`: calls and tokens per model, beside the existing
  per-source `tokens`.
- `retries`: backoff retries of model calls by status, by kind of call and by
  model, with the seconds of backoff. gem-agent records a retry from v0.102.0
  and lagent does not; `sessions_recording` says how many sessions could hold
  one, and `not_recorded` says the rest.
- `not_recorded` for lagent now says that a cached count of 0 may mean the
  server reported none.

## [0.2.0] - 2026-11-03

### Added

- `run_sec` per built-in tool and for MCP calls: the time from the call's
  last approval decision to its result, leaving out the risk review and the
  wait at the prompt; calls that did not run are left out.
- `approvals.gate.operator_wait_sec`: how long approval prompts waited for
  the operator's answer.
- `tools.multi_call_by_kind`: responses with several calls by kind
  (`read_only`, `with_mcp_or_web`, `other`), with the seconds their calls
  took one after another and would have taken at once.
- `anomalies.unmatched_decisions`: decision records that do not name the
  call awaiting its result.

These fields are added to report schema 1; nothing was renamed or removed.

### Fixed

- gem-agent's `connection_lost` and `library_log` and lagent's
  `window_full` records were counted as unknown record kinds.
- A shell lane written in another case or with surrounding space (`"Read"`)
  was counted as `other`, and an empty one as `other` rather than
  `unrecorded`; lanes are now read the way the runtimes read them.

## [0.1.0] - 2026-10-30

### Added

- First release. Command line: `--runtime`, `--since`, `--json`,
  `--mcp-names`, `--sessions-dir`, `--version` (and `version`);
  darwin/arm64 only.
- Transcript reading for gem-agent and lagent (schema 2), tolerant of torn
  and unreadable lines and of model-written arguments of the wrong type; the
  sessions root may be a symbolic link, links below it are not followed, and
  unreadable directories are counted.
- Aggregation into a fixed report (sessions, turns, context, tokens, tools,
  shell lanes, approvals, interventions, modes, anomalies), with the facts
  the transcripts do not hold listed under `not_recorded` with their reason.
  Strings from a transcript reach the report only as members of a closed set;
  version and model names are held to narrow patterns. Turns exclude the
  messages the runtimes inject. Report times are UTC.
- Table and JSON output from the same data; the table walks the JSON.
