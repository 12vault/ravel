# Graph fragments

Agents extend the graph through versioned JSON fragments. Never edit `graph.json` directly.

```json
{
  "version": 1,
  "source": "code-analyzer",
  "nodes": [
    {
      "id": "python://app.run",
      "kind": "function",
      "name": "run",
      "path": "app.py",
      "startLine": 12,
      "meta": {"confidence": "extracted", "language": "python"}
    }
  ],
  "edges": [
    {
      "kind": "defines",
      "from": "file://app.py",
      "to": "python://app.run",
      "meta": {"confidence": "extracted"}
    }
  ]
}
```

Run `ravel ingest fragment.json`. The CLI rejects duplicate node IDs, missing fields, and edges with unknown endpoints.

Use stable, language-prefixed IDs. Popular and uncommon languages use the same node vocabulary: `module`, `class`, `interface`, `function`, `method`, `variable`, and `type`. Language support is determined by the analyzing agent's ability and evidence, not a hardcoded allowlist.

Use semantic kinds `concept`, `domain`, `flow`, `step`, and `tour`. Use `depends_on`, `belongs_to`, `explains`, `flows_to`, `affects`, and `part_of` for semantic edges.
