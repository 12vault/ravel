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
- Agent-produced nodes or edges: read `references/fragments.md`, write a fragment, then run `ravel ingest <fragment.json>`.

## Core workflow

1. Read `.reporavel/report.md` when present. Before broad source search, use `ravel context "<question>"`; use `ravel query` when only a symbol or path lookup is needed.
2. Run `ravel audit <target>` before a first build and show the user what will be read.
3. Ask before building a missing or stale graph.
4. Run `ravel build <target>` after consent.
5. Read `references/orchestration.md`, run `ravel plan <route> --json`, and dispatch every ready role. Parallelize only tasks whose dependencies are complete.
6. Require each role to return one fragment file. Run `ravel ingest` after each wave; stop the workflow if validation fails.
7. Answer from graph evidence and cite `path:startLine` when available.

## Specialized roles

Use the seven executable role prompts in `agents/`. On hosts with native subagents, dispatch those roles directly. Otherwise execute the same prompts sequentially in isolated passes.

1. `project-scanner`: inventory languages, frameworks, manifests, entry points, and boundaries.
2. `code-analyzer`: extract symbols and explicit code relationships for any language.
3. `architecture-analyzer`: infer layers, components, decisions, and cross-cutting concerns.
4. `tour-builder`: create dependency-ordered explanations and onboarding tours.
5. `graph-reviewer`: reject missing endpoints, unsupported claims, duplicates, and weak provenance.
6. `domain-analyzer`: model business domains, flows, steps, actors, and affected code.
7. `document-analyzer`: extract concepts, claims, citations, schema entities, and code links from docs and PDFs.

Keep each role scoped to explicit files from the audited corpus. Do not let agents scan ignored or secret-like files.

## Bootstrap

Probe the required command with `ravel context --help`; a successful `ravel version` is not enough because an older binary may lack the connected retriever. If the probe fails, use the packaged launcher at `scripts/ravel.sh` on macOS/Linux or `scripts/ravel.ps1` on Windows for every Ravel command. The launcher selects the bundled binary for the current operating system and architecture, including the repository marketplace copy when this is the source checkout. It runs in place: do not download or install anything. If neither command is available, stop and ask before updating or installing Ravel. After resolving the command, continue from `ravel audit`.

## Safety

- Keep source and graph artifacts local unless the user explicitly authorizes an external model or service.
- Never read `.env`, keys, certificates, credential stores, or ignored files. Ravel applies `.gitignore`, credential-directory, and key-material exclusions before content reads; stop if an audit contradicts that policy.
- Include every evidence file in fragment `sourcePaths`. Tag direct evidence with `confidence: extracted` and `evidence: path:line`; tag agent reasoning with `confidence: inferred` and a concrete `rationale`.
- Treat unresolved and ambiguous relationships honestly.
- Never install hooks, dependencies, models, or integrations without consent.
