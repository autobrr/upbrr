---
sidebar_position: 1
title: Documentation Automation
---

# Documentation automation

The docs site includes two layers of local-first AI support:

- page-level AI actions in the rendered Docusaurus docs
- local scripts that collect repository context and generate reviewable drafts with a local LLM

Generated files are written under `documentation/.generated/`, which is ignored by git. Drafts must be reviewed manually before they are copied into `documentation/docs`.

## Page-level actions

Every docs page includes a menu for:

- copying the page as Markdown
- viewing the raw Markdown source
- opening the page prompt in ChatGPT, Claude, or Perplexity

The prompt includes the page title, published page URL, and raw Markdown URL so the docs page remains the source of truth.

## Collect context

Run:

```bash
pnpm run docs:context
```

This writes `documentation/.generated/context/upbrr-doc-context.json` with:

- repository metadata
- CLI flags parsed from `cmd/upbrr/cli_options.go`
- top-level config sections parsed from `internal/config/defaults/example.yaml`
- current docs page titles and headings
- selected Go source inventory

## Check coverage

Run:

```bash
pnpm run docs:check
```

The check refreshes generated context, rejects duplicate docs page titles, and reports CLI flags that are not mentioned in docs yet.

Set `DOCS_STRICT=1` to make undocumented non-alias CLI flags fail the check.

## Generate a draft

The default generator provider is Codex CLI. It runs non-interactively in read-only mode, asks for a schema-shaped final response, and writes the result into ignored draft files.

Run:

```bash
pnpm run docs:context
pnpm run docs:generate -- --topic "document unattended CLI safety"
```

By default the Codex provider uses:

- `DOCS_LLM_PROVIDER=codex`
- `DOCS_CODEX_MODEL=gpt-5.3-codex`
- `DOCS_CODEX_COMMAND=codex.cmd` on Windows, or `codex` elsewhere

Equivalent CLI flags are supported:

```bash
pnpm run docs:generate -- --provider codex --codex-model gpt-5.3-codex --topic "document CLI queue usage"
```

The generator passes `documentation/schemas/doc-draft.schema.json` to `codex exec --output-schema`, stores Codex's final JSON response, validates it, and emits Markdown plus JSON under `documentation/.generated/drafts/`. The schema intentionally uses a strict, simple front matter object so it is accepted by Codex structured output.

## Ollama fallback

For Ollama's native API, run:

```bash
DOCS_LLM_PROVIDER=ollama pnpm run docs:generate -- --topic "document unattended CLI safety"
```

The local LLM defaults are:

- `DOCS_LLM_BASE_URL=http://127.0.0.1:11434`
- `DOCS_LLM_MODEL=llama3.1:8b`
- `DOCS_LLM_NUM_CTX=32768`
- `DOCS_LLM_KEEP_ALIVE=10m`

The Ollama provider calls `/api/chat` with a JSON schema in `format`, `temperature: 0`, `seed: 1`, and `options.num_ctx` so drafts are more deterministic and have enough context for repository excerpts.

Equivalent CLI flags are also supported:

```bash
pnpm run docs:generate -- --provider ollama --base-url http://127.0.0.1:11434 --model llama3.1:8b --num-ctx 32768 --topic "document CLI queue usage"
```

The generator asks the local model for schema-shaped JSON and writes JSON plus Markdown drafts under `documentation/.generated/drafts/`.

Review the generated draft before publishing it. Do not let generated content introduce behavior that is not supported by the repository context.

## Local model guidance

Small general-purpose models may produce plausible but unsupported commands. Prefer a code- or documentation-capable model with a larger context window, then keep draft review strict.

If Ollama is truncating the repository context, increase context length in the Ollama app settings or set `DOCS_LLM_NUM_CTX` for the request. Larger context requires more memory.
