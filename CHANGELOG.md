# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/).

## [Unreleased]

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

- gem-agent's `connection_lost` and lagent's `window_full` records were
  counted as unknown record kinds.

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
