# Graph fragments

Agents extend the graph through versioned JSON fragments. Never edit `graph.json` directly.

```json
{
  "version": 1,
  "source": "code-analyzer",
	"sourcePaths": ["app.py"],
  "nodes": [
    {
      "id": "python://app.run",
      "kind": "function",
      "name": "run",
      "path": "app.py",
      "startLine": 12,
	  "meta": {"confidence": "extracted", "evidence": "app.py:12", "language": "python"}
    }
  ],
  "edges": [
    {
      "kind": "defines",
      "from": "file://app.py",
      "to": "python://app.run",
	  "meta": {"confidence": "extracted", "evidence": "app.py:12"}
    }
  ]
}
```

Run `ravel ingest fragment.json`. The CLI rejects duplicate node IDs, missing fields, source paths outside the current graph, unsupported confidence values, and edges with unknown endpoints. Ravel records the current hash of every `sourcePaths` entry and invalidates the fragment's knowledge when any evidence file changes.

Every extracted node and edge must provide `meta.evidence`, normally as `path:line`. Every inferred node and edge must provide `meta.rationale` explaining the reasoning. Do not claim inferred knowledge is extracted.

Use stable, language-prefixed IDs. Popular and uncommon languages use the same node vocabulary: `module`, `class`, `interface`, `function`, `method`, `variable`, and `type`. Language support is determined by the analyzing agent's ability and evidence, not a hardcoded allowlist.

Use semantic kinds `concept`, `domain`, `flow`, `step`, and `tour`. Use `depends_on`, `belongs_to`, `explains`, `flows_to`, `affects`, and `part_of` for semantic edges.
