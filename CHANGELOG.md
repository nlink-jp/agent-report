# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/).

## [Unreleased]

### Added

- Project scaffold: RFP (en/ja), command line with flag validation
  (`--runtime`, `--since`, `--json`, `--mcp-names`, `--sessions-dir`,
  `--version`), darwin/arm64 build and release targets.
- Transcript reading for gem-agent and lagent (schema 2), tolerant of torn
  and unreadable lines; symbolic links are not followed.
- Aggregation into a fixed report (sessions, turns, context, tokens, tools,
  shell lanes, approvals, interventions, modes, anomalies). Strings from a
  transcript reach the report only as members of a closed set.
- Table and JSON output from the same data; the table walks the JSON.
- `--mcp-names` (planned for Phase 2, included now).
