# agent-report

A usage summary of [gem-agent](https://github.com/nlink-jp/gem-agent) and
[lagent](https://github.com/nlink-jp/lagent) sessions, made on your own
machine for you to attach to a report.

> **In development.** The command line is in place; reading and counting the
> transcripts is not implemented yet. See the [RFP](docs/en/agent-report-rfp.md)
> for the plan.

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
  was launched (`-p`, `--auto`) is not in the transcripts, so the report says so.

## Usage

```sh
agent-report                          # both runtimes, last 30 days, as a table
agent-report --runtime lagent --since all
agent-report --json > report.json     # the same items as JSON
```

| Flag | Default | Meaning |
|---|---|---|
| `--runtime gem-agent\|lagent\|all` | `all` | Runtime to report on |
| `--since <n>d\|YYYY-MM-DD\|all` | `30d` | Period to count |
| `--json` | off | Emit JSON instead of the table |
| `--mcp-names` | off | Include call counts per MCP server name |
| `--sessions-dir <path>` | each runtime's default | Read transcripts from another directory (single `--runtime` only) |
| `--version` | | Print the version |

Default transcript locations: `~/.local/state/gem-agent/sessions` and
`~/.local/state/lagent/sessions`.

Exit codes: `0` success, `1` runtime error, `2` usage error.

## Supported platform

macOS on Apple Silicon only, like the two runtimes whose transcripts it reads.

## Build

```sh
make build    # outputs dist/agent-report
make test
```

## License

MIT — see [LICENSE](LICENSE).
