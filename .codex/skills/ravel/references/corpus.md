# Documents, PDFs, and schemas

## Markdown and text

Ravel extracts Markdown headings and links deterministically. Ask `document-analyzer` to add claims, concepts, decisions, and relationships that require interpretation.

## PDFs

Run `ravel tools` to discover local PDF and document utilities, then `ravel extract <audited-path.pdf>`. Ravel prefers `pdftotext`, falls back to `mutool`, writes only under `.reporavel/corpus/`, and preserves page breaks. Never send the PDF externally without consent. Give the extracted local text to `document-article-analyzer`, preserve page numbers in node metadata, then ingest its document, section, concept, and citation fragment. If no local extractor exists, report the missing tool instead of silently uploading the PDF.

## Schemas

Ravel deterministically extracts SQL tables, views (including materialized views), columns, indexes, and explicit foreign keys. It also resolves conservative `FROM` and `JOIN` references from tables and views to unambiguous physical tables, including references reached through non-recursive CTE bodies. Stable schema-object IDs are scoped to the containing directory so declarations can move between migration files without changing identity. Ambiguous names, CTE aliases, table-valued functions, and references that appear only in comments or string literals stay unresolved instead of becoming speculative edges.

`ravel tools` reports locally available SQLite, PostgreSQL, and MySQL inspection tools. Ask `document-article-analyzer` or `domain-analyzer` to interpret ownership, bounded contexts, business meaning, data flows, and links to application code. An extracted foreign-key edge proves the declared reference, not the business meaning of the relationship. Mark parser facts `extracted` and agent interpretation `inferred`.

## Mixed corpus

Join code, docs, PDFs, and schemas in one graph. Prefer explicit citations, matching identifiers, manifest references, and schema usage before semantic inference.

For DOCX, ODT, and RTF sources, use `ravel extract`; it runs local Pandoc only after the explicit command. Plain text and Markdown use the built-in copier. Extraction refuses paths that are absent from the audited graph.
