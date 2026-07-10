---
name: reporavel
description: Build or query a local RepoRavel code graph. Use when the user invokes $reporavel or /reporavel, asks to map a repository, or wants graph-first codebase analysis.
---

Rules:
- Prefer `.reporavel/report.md`, `.reporavel/graph.json`, `.reporavel/files.json`, and `.reporavel/symbols.json` before reading arbitrary source files.
- If graph data is missing, ask the user before running `reporavel build .`.
- Never install dependencies automatically.
- Never send repository contents to external services.
- Never read `.env`, private keys, certificates, or ignored files.
- Use only the local `reporavel` binary for graph operations.
- Treat unresolved calls as unresolved; do not invent a resolved target.

Allowed commands:
- Build graph: `reporavel build .`
- Audit reads first: `reporavel audit .`
- Print report: `reporavel report`
- Query graph: `reporavel query "<query>"`
- Explain symbol/file: `reporavel explain "<symbol-or-path>"`
- Find path: `reporavel path "<from>" "<to>"`
- Check safety defaults: `reporavel doctor`

Invocation:
- `$reporavel .` or `/reporavel .`: audit the target, ask before the first build, then build and summarize the graph.
- `$reporavel query <text>` or `/reporavel query <text>`: query an existing graph without rebuilding it.
- If the user explicitly requests automatic refresh, offer `reporavel hook install`; never install hooks without consent.

Default workflow:
1. Read `.reporavel/report.md` if it exists.
2. Use `reporavel query`, `reporavel explain`, or `reporavel path` for targeted questions.
3. Ask before rebuilding stale or missing graph data.
4. Read source files only when graph evidence is not enough.
