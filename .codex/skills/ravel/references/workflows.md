# Workflows

## `tech`

1. Build or update the structural graph.
2. Have `project-scanner` identify languages, frameworks, manifests, services, data stores, and infrastructure.
3. Have `code-analyzer` fill missing language semantics in bounded file batches.
4. Ingest both fragments and report technologies with evidence paths.

## `understand`

1. Run `project-scanner`, then `code-analyzer` in parallel batches when supported.
2. Run `architecture-analyzer` only after structural fragments are ingested.
3. Run `domain-analyzer` against the enriched graph and relevant docs/schemas.
4. Run `graph-reviewer` last. Remove or downgrade unsupported inferred edges.
5. Produce architecture, domain, flow, risk, and suggested-question views.

## `learn`

1. Query entry points, high-degree nodes, domains, and flows.
2. Ask `tour-builder` for a dependency-ordered tour with beginner, contributor, and maintainer depths.
3. Require every tour step to cite graph node IDs and source locations.

## `diff`

1. Determine changed files without reading secret content.
2. Map changed file nodes to incoming and outgoing graph paths.
3. Ask `architecture-analyzer` and `domain-analyzer` to identify technical and business impact.
4. Separate direct dependents from inferred ripple effects.

## `update`

At the beginning of each skill task, run `ravel update <target>` once when `.reporavel/graph.json` already exists. The update is local and hash-aware: use `.reporavel/files.json` and the changed paths recorded by the command to scope re-analysis. Rebuild deterministic data first, then rerun only agent roles whose evidence files changed. Do not rebuild a missing graph without consent.

Continuous refresh remains opt-in. Use `ravel watch --interval 2s <target>` for saved-file changes during an active coding session, or `ravel hook install <target>` for post-commit and post-checkout refresh. Never start either automatically from the skill.

## Optional community descriptions

1. Keep clustering deterministic. Choose granularity and hub controls before description work.
2. Run `ravel community --template` to export current stable community IDs.
3. Ask `community-describer` to fill only `description` and `rationale` using the bounded graph context supplied by the caller.
4. Review the JSON, then run `ravel community describe <file>`.
5. Treat every description as inferred. Never let AI output change IDs, membership, deterministic names, granularity, or hub thresholds.
6. On later updates, preserve descriptions only for exact membership. Transfer display labels only through Ravel's one-to-one overlap remapper; review provisional labels before relying on them.
