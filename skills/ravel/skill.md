---
name: ravel
description: Build or query a local RepoRavel code graph. Use when the user invokes $ravel or /ravel, asks to map a repository, or wants graph-first codebase analysis.
---

Rules:
- Prefer `.reporavel/report.md`, `.reporavel/graph.json`, `.reporavel/files.json`, and `.reporavel/symbols.json` before reading arbitrary source files.
- If graph data is missing, ask the user before running `ravel build .`.
- Never install dependencies automatically.
- Never send repository contents to external services.
- Never read `.env`, private keys, certificates, or ignored files.
- Use only the local `ravel` binary for graph operations.
- Treat unresolved calls as unresolved; do not invent a resolved target.

Allowed commands:
- Build graph: `ravel build .`
- Audit reads first: `ravel audit .`
- Print report: `ravel report`
- Query graph: `ravel query "<query>"`
- Explain symbol/file: `ravel explain "<symbol-or-path>"`
- Find path: `ravel path "<from>" "<to>"`
- Check safety defaults: `ravel doctor`

Invocation:
- `$ravel .` or `/ravel .`: audit the target, ask before the first build, then build and summarize the graph.
- `$ravel query <text>` or `/ravel query <text>`: query an existing graph without rebuilding it.
- If the user explicitly requests automatic refresh, offer `ravel hook install`; never install hooks without consent.

Default workflow:
1. Read `.reporavel/report.md` if it exists.
2. Use `ravel query`, `ravel explain`, or `ravel path` for targeted questions.
3. Ask before rebuilding stale or missing graph data.
4. Read source files only when graph evidence is not enough.
