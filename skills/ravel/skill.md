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
- Queries, paths, and explanations: prefer `ravel query`, `ravel path`, and `ravel explain` against the existing graph.
- Agent-produced nodes or edges: read `references/fragments.md`, write a fragment, then run `ravel ingest <fragment.json>`.

## Core workflow

1. Read `.reporavel/report.md` when present.
2. Run `ravel audit <target>` before a first build and show the user what will be read.
3. Ask before building a missing or stale graph.
4. Run `ravel build <target>` after consent.
5. Use specialized agents when available and authorized; otherwise run the same roles sequentially.
6. Validate every agent result by ingesting its fragment through `ravel ingest`.
7. Answer from graph evidence and cite `path:startLine` when available.

## Specialized roles

Use five core roles plus two source-specific roles:

1. `project-scanner`: inventory languages, frameworks, manifests, entry points, and boundaries.
2. `code-analyzer`: extract symbols and explicit code relationships for any language.
3. `architecture-analyzer`: infer layers, components, decisions, and cross-cutting concerns.
4. `tour-builder`: create dependency-ordered explanations and onboarding tours.
5. `graph-reviewer`: reject missing endpoints, unsupported claims, duplicates, and weak provenance.
6. `domain-analyzer`: model business domains, flows, steps, actors, and affected code.
7. `document-analyzer`: extract concepts, claims, citations, schema entities, and code links from docs and PDFs.

Keep each role scoped to explicit files from the audited corpus. Do not let agents scan ignored or secret-like files.

## Safety

- Keep source and graph artifacts local unless the user explicitly authorizes an external model or service.
- Never read `.env`, keys, certificates, credential stores, or ignored files.
- Tag direct evidence with `confidence: extracted`; tag agent reasoning with `confidence: inferred`.
- Treat unresolved and ambiguous relationships honestly.
- Never install hooks, dependencies, models, or integrations without consent.
