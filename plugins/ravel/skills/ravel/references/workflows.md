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

Use file hashes from `.reporavel/files.json` to scope re-analysis. Rebuild deterministic data first, then rerun only agent roles whose source files changed. Git hooks are opt-in via `ravel hook install`.
