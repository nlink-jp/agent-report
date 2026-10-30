# CLAUDE.md — agent-report

**Organization rules (mandatory): https://github.com/nlink-jp/.github/blob/main/CONVENTIONS.md**

## Canonical specification

The RFP (`docs/{en,ja}/agent-report-rfp*.md`) is the canonical spec. Do not
widen the scope without updating it.

## Non-negotiable rules

- **Tests are mandatory** — write them with the implementation.
- **Never `go build` directly** — always `make build` (outputs to `dist/`).
- **Docs in sync** — update `README.md` and `README.ja.md` together.
- **Small, typed commits** — `feat:`, `fix:`, `test:`, `chore:`, `docs:`, `refactor:`.
- **No real transcripts in the repository** — test fixtures are synthetic.
- **Standard library only** — no third-party modules.

## Rules specific to this tool

- **The output carries no free text from a transcript.** Every emitted item is
  a number, an enumerated value, or a name matched against a known list. A new
  item that would copy a string out of a transcript is out of scope, not a
  redaction problem to solve.
- **Never send anything.** No network code.
- **Never change the runtimes.** A fact gem-agent or lagent does not record is
  reported as "not recorded"; it is not filled by asking the runtimes to record
  more on this tool's behalf.
- **Read only.** Never write, move or delete a transcript.
