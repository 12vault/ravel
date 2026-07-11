# RepoRavel

![RepoRavel repository topology](assets/reporavel-hero.png)

**Untangle your codebase.**

RepoRavel builds a local graph of a repository so developers and coding agents can understand its shape before reading files one by one.

It maps directories, files, packages, symbols, imports, definitions, and calls into inspectable JSON artifacts and a concise Markdown report. Everything runs locally: no network requests, hosted service, embeddings, or language model required.

> [!NOTE]
> RepoRavel is an early v0.1 project. Go has AST-level symbol and call analysis today. Other recognized file types appear in the repository topology but do not yet receive semantic analysis.

## Why RepoRavel?

Coding agents are good at reading a file. The harder problem is knowing which file matters, what calls it, and how it connects to the rest of the repository.

RepoRavel creates that missing map:

- Find entry points and central packages.
- Trace calls and definitions between symbols.
- Search the graph without rescanning source files.
- Generate a suggested reading order.
- Give coding agents compact, local repository context.
- Audit what will be read before building anything.

## Quick start

RepoRavel requires Go 1.26.5 or newer.

Install the CLI from a clone:

```sh
git clone https://github.com/12vault/ravel.git
cd ravel
go install ./cmd/ravel
```

Register the bundled skill with your AI coding assistant:

```sh
# Codex user-wide install
ravel install --platform codex

# Or keep the skill and integration files inside the current project
ravel install --project --platform codex
```

Then invoke the skill in Codex:

```text
$ravel .
```

Assistants that use slash commands accept `/ravel .` instead. The installer also supports `claude`, `codebuddy`, `opencode`, `kilo`, `copilot`, `vscode`, `aider`, `openclaw`, `droid`, `trae`, `trae-cn`, `gemini`, `hermes`, `kimi`, `amp`, `agents`, `kiro`, `pi`, `cursor`, `devin`, and `antigravity` skill locations. Use `--project` for a repository-scoped installation.

Then run it from a repository:

```sh
cd your-repository

# Confirm the installed version.
ravel version

# Preview which files RepoRavel will read.
ravel audit .

# Build the local graph in .reporavel/.
ravel build .

# Print the generated overview.
ravel report

# Generate a self-contained interactive graph.
ravel dashboard
```

Or build a local binary:

```sh
go build -o ravel ./cmd/ravel
```

## Explore the graph

Search for files, packages, types, functions, or methods:

```sh
ravel query "SessionManager"
```

Explain a file or symbol and show its immediate relationships:

```sh
ravel explain "internal/auth/session.go"
```

Find a path between two graph nodes:

```sh
ravel path "main" "CreateSession"
```

Add `--json` to `query`, `explain`, or `path` when another tool will consume the result.

Natural-language wording is accepted for lexical graph search:

```sh
ravel query "which parts handle authentication?"
```

## Generated artifacts

`ravel build .` writes these files to `.reporavel/` by default:

| File | Purpose |
| --- | --- |
| `report.md` | Human-readable architecture summary and reading order |
| `graph.json` | Complete node, edge, metric, and diagnostic graph |
| `files.json` | Scanned files, hashes, sizes, languages, and ignored paths |
| `symbols.json` | Extracted functions, methods, types, variables, and related symbols |

`ravel dashboard` additionally creates `graph.html`, a dependency-free local dashboard with search, kind filters, node details, and relationship navigation.

The graph models repository containment, code symbols, documents, schema entities, technical architecture, and business domains. The Go parser, Markdown parser, and SQL schema parser add deterministic facts. Agent-produced facts for any language or corpus enter through validated, provenance-tagged graph fragments:

```sh
ravel ingest fragment.json
```

## Audit-first safety

RepoRavel is deliberately small and local.

- `ravel audit .` lists what will be analyzed and ignored.
- Network access, shell execution, LLM calls, and subagents are not used.
- `.env` files, private-key formats, databases, archives, binary media, dependency folders, and common build output are ignored by default.
- Default limits are 1 MiB per file and 100 MiB total input.
- Output goes to `.reporavel/` unless another directory is explicitly selected.
- Unresolved calls stay unresolved instead of being presented as certain matches.

Check the active defaults at any time:

```sh
ravel doctor
```

These defaults reduce accidental exposure; they are not a substitute for reviewing what exists in a repository before processing or sharing generated artifacts.

## Configuration

Create `.reporavel.yaml` with documented defaults:

```sh
ravel init
```

Useful command-line overrides include:

```sh
ravel audit --max-file-size 2097152 .
ravel build --out /tmp/ravel-output .
ravel build --no-call-graph .
ravel update .
```

Configuration is strict: unknown settings, invalid values, and options that are not implemented yet return an error. Set `analysis.go` to `false` for topology-only output. The `output.json` and `output.markdownReport` switches control which artifacts are written.

## Agent workflow

The repository includes [`skills/ravel/skill.md`](skills/ravel/skill.md), a progressive agent workflow for technical maps, architecture understanding, business domains, change impact, documents, schemas, and dependency-ordered learning tours.

The intended loop is:

1. Audit the repository or corpus.
2. Build deterministic graph facts with user consent.
3. Use specialized agents for language-independent code, architecture, domain, tour, and document analysis.
4. Validate and merge every agent fragment with `ravel ingest`.
5. Use `query`, `explain`, `path`, and `dashboard` for exploration.

### Always-on integration and hooks

Skill installation and hooks are separate. A project-scoped Codex install writes the skill, a marked section in `AGENTS.md`, and a `PreToolUse` entry in `.codex/hooks.json`. The hook reminds Codex to query an existing graph before broad source searches. It does not build a graph by itself. Codex may ask you to review and trust the new hook before it runs.

These equivalent commands manage only the Codex always-on files:

```sh
ravel codex install
ravel codex uninstall
```

Automatic graph refresh is opt-in. Install Git `post-commit` and `post-checkout` hooks from the repository:

```sh
ravel hook install
ravel hook status
ravel hook uninstall
```

The Git hooks launch `ravel update .` in the background and write failures to the temporary `ravel-hook.log` file. Existing hook content is preserved.

## Current scope

RepoRavel currently provides:

- Popular-language file and directory topology without a language allowlist at the agent layer.
- Go AST plus deterministic Markdown heading/link and SQL table/column extraction.
- Validated graph-fragment ingestion for agent analysis of any programming language.
- Code, document, PDF-corpus, schema, concept, domain, flow, step, and tour node vocabularies.
- Natural-language lexical search, complete relationship explanations, and shortest-path queries.
- Incremental graph maintenance that preserves unchanged agent enrichment.
- A self-contained interactive HTML dashboard that needs no server or AI.
- Seven specialized agent roles: scanner, code, architecture, tour, review, domain, and document.
- JSON output for tools and Markdown output for humans.
- Deterministic artifact ordering for reviewable results.
- Global/project skill installation, Codex hooks, and opt-in Git update hooks.

Not implemented yet:

- Built-in deterministic AST analyzers for popular languages beyond Go; agents currently provide those semantics.
- Full Go type resolution across every call form.
- Filesystem watch mode and truly incremental parser execution.
- Built-in PDF text extraction; the document agent currently uses host-local PDF tooling.
- Automatic execution of the specialized agents from the standalone CLI.
- Native integration tests for every supported assistant platform.
- Prebuilt release binaries, Claude marketplace publishing, and a Codex skills marketplace package.
- LOCOMO, LongMemEval, and code-graph benchmark suites.
- An MCP server or editor integration.
- A production SQLite index.

## Development

Run the checks:

```sh
go test ./...
go vet ./...
```

The test fixture under `testdata/simple-go-service/` covers repository topology and Go call extraction.

## License

RepoRavel is available under the [MIT License](LICENSE).
