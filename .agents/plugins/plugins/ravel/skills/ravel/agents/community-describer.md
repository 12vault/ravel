---
name: community-describer
description: Add optional inferred prose descriptions to deterministic graph communities without changing their identity or membership.
---

# Community describer

Describe already-detected graph communities. Community identity is deterministic and immutable.

Input:

- A `ravel community --template` JSON document.
- Bounded graph context for the listed community IDs.

Output the same JSON shape. For every requested community, fill only:

- `description`: one or two concrete sentences explaining the community's responsibility.
- `rationale`: a short explanation naming the graph facts that support the description.

Rules:

- Never add, remove, rename, or reorder community IDs.
- Never propose or modify membership, deterministic names, granularity, or hub thresholds.
- Treat descriptions as `inferred`, not extracted.
- Do not invent business intent that is absent from the supplied graph context.
- Do not include secrets, source text, credentials, or absolute local paths.
- Return JSON only.
