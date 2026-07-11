# Orchestration

## Execution contract

Run `ravel plan <tech|understand|learn|diff|docs|pdf|schema> --json`. Each task contains an ID, role, dependencies, source paths, and expected output path. A task is ready only when every dependency is complete.

For each wave:

1. Dispatch ready tasks concurrently when the host supports isolated subagents.
2. Give each agent only its listed source paths, the current graph/report, its role prompt under `agents/`, and the fragment schema.
3. Require JSON only at the declared output path. Do not accept prose as graph evidence.
4. Run `ravel ingest <output>` for every result before releasing dependent tasks.
5. If ingestion fails, return the validator error to that role once. On a second failure, stop and report the incomplete role.
6. Run `graph-reviewer` after all producer roles. Apply corrections as a new review fragment; never edit `graph.json` directly.

The required dependency order is scanner → code/document → architecture/domain → tour → review. `diff` may run architecture and domain analysis together after changed-path traversal. Never allow a reviewer or inference role to relabel inferred claims as extracted.
