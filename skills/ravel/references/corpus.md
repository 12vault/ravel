# Documents, PDFs, and schemas

## Markdown and text

Ravel extracts Markdown headings and links deterministically. Ask `document-analyzer` to add claims, concepts, decisions, and relationships that require interpretation.

## PDFs

Treat PDFs as local corpus files. Extract text locally with the host's PDF tooling, preserve page numbers in node metadata, and never send the PDF externally without consent. Create `document`, `section`, `concept`, and citation nodes, then ingest the fragment.

## Schemas

Ravel deterministically extracts basic SQL tables and columns. Ask `document-analyzer` or `domain-analyzer` to add foreign-key meaning, bounded contexts, ownership, data flows, and links to application code. Mark parser facts `extracted` and inferred business meaning `inferred`.

## Mixed corpus

Join code, docs, PDFs, and schemas in one graph. Prefer explicit citations, matching identifiers, manifest references, and schema usage before semantic inference.
