# Ravel

![Ravel repository topology](assets/reporavel-hero.png)

**Untangle your codebase.**

Ravel builds a local knowledge graph of code and documents so developers and coding agents can understand a system before reading files one by one.

The Go binary provides a fast, offline evidence layer. The bundled skill adds language-independent agent analysis for architecture, domains, flows, tours, documents, PDFs, and schemas. Agent enrichment is optional, validated, provenance-tagged, and never confused with parser-extracted facts.

> [!NOTE]
> Native deterministic extraction currently uses Go AST, Markdown structure, and SQL schema parsing. The agent skill analyzes popular programming languages without a fixed allowlist. Ravel states both layers explicitly because deterministic evidence and agent inference have different confidence and safety properties.

## Why Ravel?

Coding agents are good at reading a file. The harder problem is knowing which file matters, what calls it, and how it connects to the rest of the repository.

Ravel creates that missing map:

- Find entry points and central packages.
- Trace calls and definitions between symbols.
- Search the graph without rescanning source files.
- Generate a suggested reading order.
- Give coding agents compact, local repository context.
- Audit what will be read before building anything.

## Quick start

Install the latest checksum-verified release on macOS or Linux:

```sh
curl -fsSL https://raw.githubusercontent.com/12vault/ravel/main/install.sh | sh
```

The installer writes to `~/.local/bin` by default. If that directory is not on `PATH`, it prints the exact command to enable it. Run that command before using `ravel`, then add it to your shell profile so future terminals can find the binary.

On Windows PowerShell:

```powershell
irm https://raw.githubusercontent.com/12vault/ravel/main/install.ps1 | iex
```

PowerShell prints the equivalent `PATH` command when `~/.local/bin` is not already available.

Release archives also contain a portable `ravel` binary that can run in place without installation. To build from source, install Go 1.26.5 or newer and clone the repository:

```sh
git clone https://github.com/12vault/ravel.git
cd ravel
go install ./cmd/ravel
```

`go install` writes to `GOBIN`, or to `$(go env GOPATH)/bin` when `GOBIN` is unset. Make sure that directory is on `PATH`. To install the project-local Codex skill straight from this checkout without installing a global binary, run:

```sh
go run ./cmd/ravel install --project --platform codex
```

Register the bundled skill with your AI coding assistant:

```sh
# Codex user-wide install
ravel install --platform codex

# Or keep the skill and integration files inside the current project
ravel install --project --platform codex
```

That command installs the complete skill bundle: orchestration instructions, seven role prompts, references, and launchers. Marketplace packages also include native Ravel binaries for macOS, Linux, and Windows on amd64 and arm64. If `ravel` is not already on `PATH`, the skill uses the matching bundled binary in place, with no initial download or separate installation.

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

# Preview which files Ravel will read.
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

Retrieve a connected, token-bounded explanation for a natural-language question:

```sh
ravel context "how does SessionManager create and persist a session?"
```

`ravel context` combines Unicode-aware BM25F/IDF ranking, multiple lexical seeds, and deterministic graph traversal. It defaults to bidirectional BFS at depth 2, suppresses expansion through non-seed super-hubs, and keeps every discovered node beside the edge that explains why it was included. The compact text payload is conservatively bounded to about 2,000 tokens by default.

Useful controls include:

```sh
ravel context --traversal dfs --direction out --max-depth 3 "authentication flow"
ravel context --relations calls,references --token-budget 1200 "who reaches CreateSession?"
ravel context --json "what imports the storage package?"
```

When no explicit `--relations` filter is supplied, Ravel can infer one from words such as “called,” “imports,” “inherits,” or “tested.” Use `--infer-relations=false` to traverse every available relationship. `--json` returns the same selected context in a machine-readable envelope; `estimatedTokens` measures the compact text payload, so JSON field-name and formatting overhead is intentionally not included.

Explain a file or symbol and show its immediate relationships:

```sh
ravel explain "internal/auth/session.go"
```

Find a path between two graph nodes:

```sh
ravel path "main" "CreateSession"
```

Add `--json` to `query`, `explain`, or `path` when another tool will consume the result.

Natural-language wording is accepted for compact lexical graph search:

```sh
ravel query "which parts handle authentication?"
```

Use `query` when a short ranked list is enough. Use `context` when the relationships around the matches are part of the answer.

Focused graph workflows are first-class commands:

```sh
ravel tech
ravel understand
ravel learn
ravel docs
ravel pdf
ravel schema
ravel diff                 # impact from the last update
ravel diff internal/api.go # impact from explicit paths
```

In an agent session, these routes also activate the specialized semantic roles and merge their evidence-tagged fragments.

## Generated artifacts

`ravel build .` writes these files to `.reporavel/` by default:

| File | Purpose |
| --- | --- |
| `report.md` | Human-readable architecture summary and reading order |
| `graph.json` | Complete node, edge, metric, and diagnostic graph |
| `files.json` | Scanned files, hashes, sizes, languages, and ignored paths |
| `symbols.json` | Extracted functions, methods, types, variables, and related symbols |

Ravel also keeps canonical update state under `.reporavel/.state/`. Disabling JSON output removes the public JSON exports but preserves this internal state so updates and queries continue to work.

`ravel dashboard` additionally creates `graph.html`, a dependency-free local dashboard with search, kind filters, node details, and relationship navigation.

Create a reviewable team bundle that omits raw source and private scanner state:

```sh
ravel share --out ravel-graph
git add ravel-graph
```

The bundle contains `graph.json`, `report.md`, `graph.html`, and a safety manifest. Review inferred content before committing it.

The graph models repository containment, code symbols, documents, schema entities, technical architecture, and business domains. The Go parser, Markdown parser, and SQL schema parser add deterministic facts. Agent-produced facts for any language or corpus enter through validated, provenance-tagged graph fragments:

```sh
ravel ingest fragment.json
```

## Audit-first safety

Ravel is local-first and audit-first.

- `ravel audit .` lists what will be analyzed and ignored.
- Analysis, graph, query, dashboard, and corpus commands make no network requests and never call an LLM. Only the explicit `ravel self-update` command accesses the release server.
- `ravel extract` may execute a discovered, allowlisted local extractor (`pdftotext`, `mutool`, or `pandoc`) only when the user invokes that command.
- Agent roles run only through the installed skill and the host assistant's normal permission model.
- `.gitignore` rules, symlinks, `.env` files, private-key formats, credential directories, databases, archives, binary media, dependency folders, and common build output are rejected before file content is read.
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

The connected retriever can also be configured once for agents and benchmarks:

```yaml
retrieval:
  traversal: bfs
  direction: both
  inferRelations: true
  relations: all
  seedLimit: 3
  maxDepth: 2
  maxNodes: 100
  hubDegreeThreshold: 0 # automatic p99 with a floor of 50; -1 disables
  tokenBudget: 2000
```

## Agent workflow

The repository includes [`skills/ravel/skill.md`](skills/ravel/skill.md), a progressive agent workflow for technical maps, architecture understanding, business domains, change impact, documents, PDFs, schemas, articles, and dependency-ordered learning tours. Installers and marketplace packages publish it as the required uppercase `SKILL.md`.

The intended loop is:

1. Audit the repository or corpus.
2. Build deterministic graph facts with user consent.
3. Run `ravel plan <route> --json` to create bounded, dependency-aware agent tasks.
4. Dispatch the seven packaged agents in ready waves and validate every returned fragment with `ravel ingest`.
5. Use `context` for bounded relationship questions, `query` for exact lookups, and `explain`, `path`, and `dashboard` for focused exploration.

Use `ravel tools` before document, PDF, or schema work. It discovers local extractors and database clients without executing them. `ravel extract <audited-file>` then processes PDF, DOCX, ODT, RTF, Markdown, or text locally into `.reporavel/corpus/`; it refuses unaudited paths. PDF content stays local unless the user separately authorizes an external service.

### Native integrations and hooks

Project installs place the portable skill in the platform's native directory. For Codex, Claude Code, Cursor, VS Code/Copilot, Gemini CLI, and OpenCode, Ravel also installs owned project instructions, rules, or hooks. Existing configuration is preserved, repeated installs are idempotent, and uninstall removes only Ravel-owned content.

Manage a native integration directly:

```sh
ravel codex install
ravel codex uninstall
ravel claude install
ravel cursor install
ravel vscode install
ravel gemini install
ravel opencode install
```

The Claude marketplace package lives in [`.claude-plugin/marketplace.json`](.claude-plugin/marketplace.json), and the Codex marketplace package lives in [`.agents/plugins/marketplace.json`](.agents/plugins/marketplace.json). Both packages are validator-clean. The direct CLI installer remains available for every supported assistant destination.

Automatic graph refresh is opt-in. Install Git `post-commit` and `post-checkout` hooks from the repository:

```sh
ravel hook install
ravel hook status
ravel hook uninstall
```

The Git hooks launch `ravel update .` in the background and write failures to the temporary `ravel-hook.log` file. Existing hook content is preserved.

For live work without Git events, use polling watch mode:

```sh
ravel watch --interval 2s .
```

Only changed hashes trigger an update. The update invalidates stale agent enrichment and records changed paths for `ravel diff`.

### Updating Ravel

Binary and manual skill installations can update together:

```sh
ravel self-update --platforms codex,claude
ravel self-update --platforms codex,claude --project
```

The command downloads the selected release archive and checksum, verifies it, atomically replaces the binary, then refreshes only the explicitly listed skill destinations. Marketplace-managed skills update through their marketplace client.

After CLI changes, run `python3 scripts/sync-packages.py` to rebuild all six native binaries and copy the refreshed skill bundle into both marketplace packages. Validate the result with `python3 scripts/test_release.py`.

macOS release jobs use `MACOS_CERTIFICATE_P12`, `MACOS_CERTIFICATE_PASSWORD`, `MACOS_SIGNING_IDENTITY`, `APPLE_ID`, `APPLE_APP_PASSWORD`, and `APPLE_TEAM_ID` repository secrets when available. Until those are configured, releases use ad-hoc signatures for integrity but are not notarized by Apple.

Maintainers prepare a synchronized release with `scripts/release.sh 0.2.0`. It updates CLI, Claude, and Codex versions, rebuilds the bundled binaries, synchronizes every packaged skill resource, runs tests and validators, and verifies that no package drift remains. Committing and pushing tag `v0.2.0` triggers the binary release workflow.

## Roadmap

- Configure Apple Developer ID signing and notarization secrets for Gatekeeper-approved macOS marketplace and release binaries.
- Replace ad-hoc macOS release signatures with mandatory Developer ID signatures after the first public MVP releases.

## Capability layers

| Capability | Offline Go binary | Agent skill |
| --- | --- | --- |
| Popular languages | Safe file topology and language inventory | Symbols, references, architecture, intent, and explanations across languages |
| Code structure | Native Go AST | Language-independent specialized code analyzer |
| Docs and schemas | Markdown and SQL extraction | Rich document, PDF, article, and schema semantics |
| Domains and flows | Validated graph model and focused views | Domain, flow, process-step, and actor inference |
| Learning | Centrality and tour graph views | Generated onboarding guides and dependency-ordered tours |
| Queries | IDF/BM25F search, bounded multi-seed BFS/DFS context, explain, shortest path, traversal, impact | Natural-language synthesis grounded in graph evidence |
| Updates | Hash-aware update, Git hooks, and watch mode | Stale enrichment invalidation and targeted re-analysis |
| Dashboard | Self-contained read-only HTML | Optional generated explanations and tours |

The distinction is intentional: a skill does not need a language allowlist, but users still need to know which facts are reproducibly parsed and which are inferred by an agent.

## Benchmarks

Run the local build/query performance suite with `./benchmarks/run.sh`. Run the checked-in relationship suite with `ravel benchmark --dataset benchmarks/ravel-retrieval.jsonl --retriever context`; use `--retriever flat` for the ranked-list baseline. [`benchmarks/datasets.json`](benchmarks/datasets.json) defines the implemented repository-question contract. Version 3 reports node recall/precision, evidence recall/precision, reciprocal rank, p50/p95 latency, compact context tokens, node and evidence recall per 1,000 tokens, truncation rate, index-build time, logical graph and dataset hashes/revisions, adapter version, Ravel version, Go version, and platform. An optional strict `--answers` ledger adds externally adjudicated accuracy, rubric key-fact coverage, total agent tokens, total spend, and provenance without retaining raw answers; see the [benchmark guide](benchmarks/README.md). Ravel does not claim native LOCOMO/LongMemEval corpus adapters and never invokes a model or judge. Every published score must retain the raw result file and configuration.

## Development

Run the checks:

```sh
go test ./...
go vet ./...
```

The test fixture under `testdata/simple-go-service/` covers repository topology and Go call extraction.

## License

Ravel is available under the [MIT License](LICENSE).
