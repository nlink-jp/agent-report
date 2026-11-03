# agent-report

A usage summary of [gem-agent](https://github.com/nlink-jp/gem-agent) and
[lagent](https://github.com/nlink-jp/lagent) sessions, made on your own
machine for you to attach to a report.

[日本語版 README](README.ja.md)

## Why

When you report a problem with gem-agent or lagent, the maintainer cannot see
how large your setup is or how you use it — and your session transcripts hold
your own work, so you cannot simply send them. agent-report reads those
transcripts and prints a fixed set of numbers: session counts, rounds per turn,
context size, per-tool call counts and durations, approval decisions, MCP
server and tool counts.

- **Numbers only.** No conversation, command, path, declared purpose or other
  free text from a transcript appears in the output. Built-in tool names are
  matched against a known list; anything else is counted as "other". MCP
  server names appear only with `--mcp-names`.
- **Nothing is sent.** The tool has no network access. You read the output,
  then hand it over yourself.
- **Read only.** Transcripts are never written, moved or deleted.
- **What is not recorded is said to be not recorded.** For example, how a run
  was launched (`-p`, `--auto`) is not in the transcripts, so the report says
  so, and says why.

## Install

```sh
brew install nlink-jp/tap/agent-report
```

Or download `agent-report-v<version>-darwin-arm64.zip` from the
[releases page](https://github.com/nlink-jp/agent-report/releases) — Developer ID
signed and Apple-notarized.

## Usage

```sh
agent-report                          # both runtimes, last 30 days, as a table
agent-report --runtime lagent --since all
agent-report --json > report.json     # the same items as JSON
agent-report version                  # same as --version
```

| Flag | Default | Meaning |
|---|---|---|
| `--runtime gem-agent\|lagent\|all` | `all` | Runtime to report on |
| `--since <n>d\|YYYY-MM-DD\|all` | `30d` | Period to count (a date is midnight in your local time) |
| `--json` | off | Emit JSON instead of the table |
| `--mcp-names` | off | Include call counts per MCP server name |
| `--sessions-dir <path>` | each runtime's default | Read transcripts from another directory (single `--runtime` only) |
| `--version` | | Print the version |

Default transcript locations: `~/.local/state/gem-agent/sessions` and
`~/.local/state/lagent/sessions`. A runtime whose default location does not
exist is reported as `missing`; a `--sessions-dir` that does not exist is an
error.

Exit codes: `0` success, `1` runtime error, `2` usage error.

## What the report holds

At the top: `schema` (the report format version), `tool`, `tool_version`, and
`period` (`since` and `until`, in UTC — the report does not carry your time
zone). Then, per runtime:

| Section | Items |
|---|---|
| `transcripts` | `found` or `missing` (the path itself is never printed) |
| `sessions` | sessions with activity in the period, resumes |
| `versions`, `models` | sessions per runtime version and per model |
| `turns` | operator turns and rounds per turn (n / p50 / p90 / max) |
| `context` | largest prompt per session, cached share of prompt, compactions |
| `tokens` | calls and tokens per call source (main loop, risk review, …) |
| `tools` | responses with calls, responses with several calls (and, by kind, their calls and the seconds they took one after another and would have taken at once), calls and elapsed / run seconds per built-in tool, calls to unknown names, MCP calls / servers / tools / elapsed / run seconds |
| `shell` | shell calls per lane (read / write / operator / unconfined / unrecorded / other), timeouts |
| `approvals` | auto-approve decisions by tier, approval-prompt decisions by source, seconds spent waiting for your answer at the prompt, hook denials |
| `interventions` | round-limit and loop interventions |
| `modes` | sessions that started with auto-approve or read-only (counted once per session, resumes included), mode changes |
| `empty_model_responses` | model responses that came back empty |
| `not_recorded` | facts the transcripts do not hold, each with the reason |
| `anomalies` | unreadable directories, files and lines, missing headers, unsupported schema, unknown record kinds, unmatched tool results, approval decisions that name a call other than the one awaiting its result |

Strings from a transcript appear only when they belong to a closed set: an
enumerated value the runtimes write, a known built-in tool name, or a
version, model or MCP server name that matches a narrow pattern — a version
is a release tag or `git describe` output (a bare commit hash is shown as
`dev`), a model is an id with at most one publisher prefix (`google/…`).
Anything else is counted under `other` (or `unrecorded` when the field is
absent). A model id you chose yourself is shown as you wrote it, so read the
`models` section before you send the report.

Turns start at a message you typed; the messages the runtimes inject (session
facts, resume notes, `!` shell escapes, notes about interrupted calls) do not
start one.

The runtimes run the calls of one model response one after another. A call's
elapsed seconds run from the end of the previous call in that response (from
the response itself for the first call) to the call's result, so they include
time spent waiting for an approval or for your answer to `ask_user`.

Run seconds start instead at the call's last approval decision, when it had
one, so they leave out the risk review and the wait at the prompt; a call
that did not run (denied at the prompt or by a hook, or skipped) is left out
of them. `ask_user` has no approval, so its run seconds are the wait for your
answer. `operator_wait_sec` is the other side: from the call's start (or its
auto-approve decision) to your answer at the prompt, whether you approved or
denied.

A one-shot run (`-p`) denies at the prompt without asking you, and the
runtimes record that as your answer; since how a run was launched is not
recorded, those denials sit in `operator_wait_sec` as waits of almost
nothing.

`multi_call_by_kind` sorts the responses with several calls into
`read_only` (built-in tools both runtimes mark as changing nothing, and
read-lane shell calls — including a shell call that names no lane, which
runs in the read lane), `with_mcp_or_web` (those plus MCP, `web_fetch` or
`web_search` calls) and `other`. For each kind, `sequential_sec` adds up the
run seconds of every call and `if_parallel_sec` adds up only each response's
longest call — what the calls would have taken had they run at once,
assuming they would not slow one another and could all start together (a
call that needs a tool loaded by an earlier call in the same response could
not). The runtimes answer every call, an interrupted one at once, so it
counts with almost no time; only a response whose results are missing
(the runtime stopped mid-response) is left out, so these responses can add
up to fewer than `multi_call_responses`.

The table and `--json` are drawn from the same data; the table is the JSON
laid out as indented lines.

## Supported platform

macOS on Apple Silicon only, like the two runtimes whose transcripts it reads.

## Build

```sh
make build    # outputs dist/agent-report
make test
make vuln     # govulncheck
```

## Documents

- [RFP](docs/en/agent-report-rfp.md) — the specification and the decisions behind it

## License

MIT — see [LICENSE](LICENSE).
