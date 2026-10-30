# RFP: agent-report

> Generated: 2026-10-30
> Status: Draft

## 1. Problem Statement

When a third party who uses gem-agent or lagent reports a defect or asks for an improvement, the maintainer has no way to learn the scale of that person's environment (tool count, context size, session length) or how they use the runtime. Their session transcripts hold business data, so they cannot be handed over as they are. agent-report reads the local transcripts and produces a fixed set of numbers that the person can inspect before attaching them to a report. The main users are external users of both runtimes; the maintainer also uses it to re-measure their own usage.

## 2. Functional Specification

### Commands / API Surface

```
agent-report [--runtime gem-agent|lagent|all] [--since <period>] [--json]
             [--mcp-names] [--sessions-dir <path>]
```

| Flag | Default | Meaning |
|---|---|---|
| `--runtime` | `all` | Which runtime to report on |
| `--since` | `30d` | Period (`30d`, `2026-09-01`, `all`) |
| `--json` | off | Emit JSON instead of the human-readable table |
| `--mcp-names` | off | Include per-MCP-server call counts (Phase 2) |
| `--sessions-dir` | each runtime's default | Read transcripts from another place (only with a single `--runtime`) |

Default transcript locations:

- gem-agent: `~/.local/state/gem-agent/sessions`
- lagent: `~/.local/state/lagent/sessions`

### Input / Output

- **Input**: JSONL session transcripts (schema 2) under the directories above. Read only; nothing is written, moved or deleted.
- **Output**: stdout. The table and the JSON are rendered from the same aggregate, so what the person checks in the table is exactly what the JSON carries.
- **Items** (per runtime, all derived from facts the transcripts record):
  - Period, session count, versions and model names seen in headers
  - Distribution of rounds per turn
  - Maximum prompt tokens per session, cache ratio, compaction count
  - Calls and duration (p50 / p90 / max) per built-in tool
  - Shell calls per lane, shell timeouts
  - Approval decisions (operator / auto, approved / denied, tier)
  - Round-limit interventions, mode changes
  - MCP calls, server count, tool count
  - Count of unknown record kinds and unreadable lines
- **Never emitted**: any free text in a transcript — conversation, commands, paths, declared purposes, notices, error messages. Built-in tool names are matched against one known list covering both runtimes; anything else counts as "other". MCP names appear only with `--mcp-names`.
- **Facts the transcripts do not hold**: how a run was launched (`-p`, `--auto`, `--allow`, …) is not recorded by the runtimes, so the report says "not recorded".

### Configuration

No configuration file. Everything is a flag.

### External Dependencies

None. Go standard library only; no network access.

## 3. Design Decisions

- **Go, standard library only**: a signed and notarized single binary spares a third party from running a script they cannot vet, and having no dependencies adds no supply-chain surface.
- **Outside the runtimes**: gem-agent ADR-0061 keeps analysis outside the runtime. A tool that measures does not change what it measures: a fact the subject does not record is reported as "not recorded", never filled by adding records to the subject.
- **No sending**: no receiving server. Three reasons: guarding a public endpoint is a heavy burden for a one-person organization, anyone could feed it fabricated data, and for users doing security work a tool that sends data out on its own erodes trust. The person hands the report over themselves.
- **Fixed numeric items only**: rules about what to redact grow without end. Limiting the output to numbers and known names from the start removes the need for such rules.
- **Separate from gem-usage-lens**: gem-usage-lens shows one's own spending; agent-report is what a third party attaches to a report — different users, different purpose. Transcript reading is reimplemented rather than shared as a library, so neither tool forces the other into a release.
- **Tolerant of format drift**: check the header schema; count and skip unknown record kinds; keep one known built-in tool list for both runtimes (their declared names overlap almost entirely; a separate list per runtime would only move a name the other runtime declares from built-in to unknown).
- **Out of scope**: sending and a receiving server, changes to the runtimes, Claude Code transcripts, cost calculation (gem-usage-lens's job), GUI, free-text output, reading configuration files (revisited in Phase 2 only if a need shows).

## 4. Development Plan

### Phase 1: Core

- Transcript reading for both runtimes, aggregation, table and JSON output, `--runtime` / `--since` / `--sessions-dir`
- Tests use synthetic transcripts only; no real data enters the repository
- Run on the maintainer's own transcripts and check the numbers against the 2026-10-30 measurement script

### Phase 2: Features

- `--mcp-names` (small enough to be brought forward into Phase 1)
- Configuration reading, with defined items, only if using the Phase 1 output in real reports shows a need

### Phase 3: Release

- README (en/ja), CHANGELOG, AGENTS.md
- Independent pre-release verification (subagent)
- Signed and notarized release, homebrew tap, util-series integration
- Asking gem-agent and lagent to point reporters at this tool in their READMEs is done by filing an issue in each project

Phase 1 and Phase 2 can each be reviewed on their own.

## 5. Required API Scopes / Permissions

None. The tool reads only files under the user's own home directory.

## 6. Series Placement

Series: util-series
Reason: util-series already holds the lens tools that read and aggregate local data (gem-usage-lens, claude-usage-lens, …). This is not a client for an external service, so cli-series does not fit.

## 7. External Platform Constraints

No external services. Two constraints:

- **Transcript format**: lagent includes its transcript and usage-record formats in its stability promise (lagent ADR-0023). gem-agent does not state whether its transcript format is part of its promise. agent-report becomes the second consumer after gem-usage-lens; whether to make the format a promise is gem-agent's decision, requested by issue if needed.
- **darwin/arm64 only**: both runtimes are macOS-only, so the tool matches them. This is an exception to the util-series standard of four targets and is recorded in the series CLAUDE.md at integration.

---

## Discussion Log

- **Origin (2026-10-30)**: six proposals for extending gem-agent as an autonomous runtime were drafted; at the user's choice their frequencies were measured first. A read-only count over local transcripts gave no support for three of them (edit journal, background jobs, parallel calls), and real use in October was a single session.
- **What is missing**: the first framing was "a usage-measurement feature is missing". gem-agent already has audit telemetry (ADR-0035), and sending to Cloud Logging was enabled; what was missing was a place for the data to accumulate and a record of how runs were launched. The user then narrowed the point: what is missing is a way for complete third parties to provide data.
- **Third-party scale**: 429 downloads over 146 releases, recently 3–6 per release including the maintainer's own verification; 0 stars; all issues filed by the maintainer. External reports arrive outside GitHub. The value lies less in statistics than in environment facts that do not reproduce locally (one external operator ran 367 tools and 130k tokens).
- **Rejected alternatives**:
  - A stats subcommand inside gem-agent: conflicts with keeping analysis outside the runtime (ADR-0061); a separate tool was the user's proposal.
  - Automatic sending to the maintainer: see "No sending" in §3.
  - Merging into gem-usage-lens: different users and purpose.
  - Adding records to the runtimes to fill gaps: the measuring tool would change its subject — the lesson from cancelling agent-activity-lens.
- **User decisions**: name agent-report; lagent included; MCP names only on request; Phase 1 reads transcripts only; default period of the last 30 days; darwin/arm64 only.
- **When it gets used**: a standing request for data is never acted on (the agent-board precedent). The tool is tied to the moment of reporting and pointed to from each runtime's README and issue template.
- **Phase 1 check (2026-10-30)**: run on the maintainer's transcripts, every number checked against the measurement script matched — gem-agent: 1821 responses with calls, 42 with several, 497 shell calls with the same per-lane split, 2 timeouts, 333 MCP calls; lagent: 149 shell calls, 46 auto-approved / 460 escalated, 456 / 4 at the prompt, 29 empty responses.
- **Independent verification (2026-10-30)**: no pass was run when the design was fixed; the design-time and pre-release passes were run together after Phase 1, as two subagents — one on code correctness against the runtimes' writers, one on the organization's rules and lessons. Accepted and fixed: lagent's per-session facts message and both runtimes' late-return notes were counted as operator turns (turns inflated by about half on real lagent data); a non-string `access` lost its whole message; the version and model patterns admitted GCP resource paths and e-mail addresses (narrowed: a version is a tag or `git describe` output, a model id has at most one `/` and no `@`); a symlinked root read as empty; unreadable subdirectories were not counted; a timeout was matched anywhere in the output; two record kinds were missing; `mode_start` was counted per process; `/clear` was not countable (moved to `not_recorded`); the `--since` boundary produced false anomalies; unreadable decision records were miscounted; `not_recorded` now states its reason; times are UTC; a missing `--sessions-dir` is an error; README status banner removed, Documents section, `.claude/` and real-data ignores, `govulncheck` target. Accepted residue: a model id the user chose is shown as written; the README asks the user to read the `models` section before sending. Deferred to the release: the `[0.1.0]` CHANGELOG entry and the series-level record of the darwin/arm64 exception.
