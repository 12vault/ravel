---
name: ravel
description: Build, enrich, query, explain, visualize, and teach codebase or document knowledge graphs with Ravel. Use when the user invokes $ravel or /ravel; asks to understand architecture, technologies, business domains, change impact, onboarding, docs, PDFs, or schemas; or wants graph-first repository analysis across any programming language.
---

# Ravel

Use the Go CLI for deterministic scanning, storage, queries, and validation. Use agents for language-independent semantics, architecture, domains, and teaching. Never claim an inferred relation is extracted.

## Route the request

- `ravel .` or `ravel tech`: build the structural graph and map technologies. Read `references/workflows.md`.
- `ravel understand`: explain architecture, intent, domains, flows, and risks. Read `references/workflows.md`.
- `ravel learn`: create dependency-ordered tours and onboarding material. Read `references/workflows.md`.
- `ravel diff`: analyze the impact of current changes. Read `references/workflows.md`.
- `ravel docs`, `ravel pdf`, or `ravel schema`: ingest non-code sources. Read `references/corpus.md`.
- Natural-language relationship questions: prefer `ravel context` for a connected, token-bounded subgraph. Use `ravel query` for a short exact/lexical result list, then `ravel path` or `ravel explain` for a named target.
- Read compact truncation reasons literally: `token_budget` may justify raising `--token-budget`, while `branch_limit` requires narrower relations/depth or a larger `--branch-fanout`. More output tokens do not reopen a pruned traversal branch.
- Reverse-impact questions: use `ravel affected <file|symbol|node-id>` for incoming callers, references, implementers, importers, and dependents. Its default impact filter excludes generic containment noise. Files bootstrap direct definitions; packages/modules/directories bootstrap directly contained files plus direct definitions, capped at 20 origins. It does not recursively expand repository or nested-directory trees; use changed-file inputs or `ravel diff` for that scope. Unresolved targets remain errors rather than guesses.
- MCP-capable hosts may launch `ravel mcp --out <graphdir>` and use its read-only `query`, `context`, `explain`, `path`, and reverse-impact `affected` tools. Keep `context` token-bounded and treat explicit unresolved results as unresolved.
- Retrieval or answer-quality evaluation: use `ravel benchmark` with the versioned repository-question JSONL contract. An optional `--answers` ledger records externally adjudicated correctness, rubric fact coverage, tokens, spend, and provenance without raw answers; Ravel never invokes a model or judge.
- Community descriptions: run `ravel community --template`, ask `community-describer` to fill only `description` and `rationale`, then import with `ravel community describe <file>`. AI content must never set community IDs, membership, deterministic names, granularity, or hub thresholds.
- Community continuity: membership-hashed IDs are authoritative. Updates may transfer deterministic display labels by one-to-one Jaccard overlap (`>=0.70` remapped, `0.40–0.70` provisional review), but descriptions transfer only when membership is identical. Substantial merges get new labels; only the strongest child of a split may inherit.
- Agent-produced nodes or edges: read `references/fragments.md`, write a fragment, then run `ravel ingest <fragment.json>`.
- Native polyglot nodes come from the pinned pure-Go Tree-sitter layer. Treat syntax captures as `extracted`, name-only target matches as `inferred`, and ambiguous or unsupported shapes as unresolved; use `code-analyzer` only to fill evidence-backed gaps rather than duplicating parser facts.

## Core workflow

1. After bootstrap, check for `.reporavel/graph.json`. When it exists, run `ravel update <target>` once before reading or querying the graph. This local, hash-aware refresh needs no additional consent; report changed paths or refresh failures, and never hide a stale-graph warning.
2. Read `.reporavel/report.md` after the refresh. Before broad source search, use `ravel context "<question>"`; use `ravel query` when only a symbol or path lookup is needed.
3. When no graph exists, run `ravel audit <target>`, show the user what will be read, and ask before the initial `ravel build <target>`.
4. When an update invalidates agent enrichment needed by the request, read `references/workflows.md` and `references/orchestration.md`, use the changed paths recorded by the update, and rerun only the affected roles. Do not redo unaffected enrichment.
5. For enrichment workflows, run `ravel plan <route> --json` and dispatch every ready role. Parallelize only tasks whose dependencies are complete.
6. Require each role to return one fragment file. Run `ravel ingest` after each wave; stop the workflow if validation fails.
7. Answer from graph evidence and cite `path:startLine` when available.

Do not start `ravel watch`, install Git hooks, or leave background processes running automatically. Suggest `ravel watch --interval 2s <target>` for saved-file refresh during active development and `ravel hook install <target>` for opt-in post-commit/post-checkout refresh. Both require explicit user consent.

## Specialized roles

Use the eight executable role prompts in `agents/`. On hosts with native subagents, dispatch those roles directly. Otherwise execute the same prompts sequentially in isolated passes.

1. `project-scanner`: inventory languages, frameworks, manifests, entry points, and boundaries.
2. `code-analyzer`: extract symbols and explicit code relationships for any language.
3. `architecture-analyzer`: infer layers, components, decisions, and cross-cutting concerns.
4. `tour-builder`: create dependency-ordered explanations and onboarding tours.
5. `graph-reviewer`: reject missing endpoints, unsupported claims, duplicates, and weak provenance.
6. `domain-analyzer`: model business domains, flows, steps, actors, and affected code.
7. `document-analyzer`: extract concepts, claims, citations, schema entities, and code links from docs and PDFs.
8. `community-describer`: add optional inferred prose descriptions to deterministic communities without changing their identity.

Keep each role scoped to explicit files from the audited corpus. Do not let agents scan ignored or secret-like files.

## Bootstrap

On the first invocation, run `scripts/ravel.sh version` on macOS/Linux or `scripts/ravel.ps1 version` on Windows, then use that launcher for every Ravel command. The launcher compares the global CLI with the version in `VERSION`, entirely locally. When the global CLI is older, it selects the bundled binary and prints a short, non-blocking update notice; surface that notice to the user and continue the task. The source-checkout launcher may select the synchronized repository marketplace binary. It never downloads, installs, or updates anything.

If the packaged launcher is absent, probe `ravel context --help`. If that fails, stop and ask before installing or updating Ravel. Never invoke `ravel update-check` or `ravel self-update` automatically during a skill task: `update-check` performs an explicit release-metadata network request, and `self-update` downloads and replaces software. After resolving the command, continue from `ravel audit`.

## Safety

- Keep source and graph artifacts local unless the user explicitly authorizes an external model or service.
- Never read `.env`, keys, certificates, credential stores, or ignored files. Ravel applies `.gitignore`, credential-directory, and key-material exclusions before content reads; stop if an audit contradicts that policy.
- Include every evidence file in fragment `sourcePaths`. Tag direct evidence with `confidence: extracted` and `evidence: path:line`; tag agent reasoning with `confidence: inferred` and a concrete `rationale`.
- Treat unresolved and ambiguous relationships honestly.
- Never install hooks, dependencies, models, or integrations without consent.
