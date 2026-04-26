import { spawnSync } from "node:child_process";
import { mkdirSync, readFileSync, writeFileSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const scriptDir = path.dirname(fileURLToPath(import.meta.url));
const docsRoot = path.resolve(scriptDir, "..");
const repoRoot = path.resolve(docsRoot, "..");
const contextPath = path.join(docsRoot, ".generated", "context", "upbrr-doc-context.json");
const draftsDir = path.join(docsRoot, ".generated", "drafts");
const schemaPath = path.join(docsRoot, "schemas", "doc-draft.schema.json");

const args = new Map();
for (let index = 2; index < process.argv.length; index += 1) {
  const arg = process.argv[index];
  if (!arg.startsWith("--")) {
    continue;
  }
  const key = arg.slice(2);
  const value = process.argv[index + 1]?.startsWith("--") ? "true" : process.argv[index + 1] || "true";
  args.set(key, value);
  if (value !== "true") {
    index += 1;
  }
}

let context;
try {
  context = JSON.parse(readFileSync(contextPath, "utf8"));
} catch {
  console.error("Docs context is missing. Run `pnpm run docs:context` before generating drafts.");
  process.exit(1);
}

const topic = args.get("topic") || "improve the most obviously incomplete upbrr docs page";
const target = args.get("target") || "documentation/.generated/drafts";
const provider = (args.get("provider") || process.env.DOCS_LLM_PROVIDER || "codex").toLowerCase();
const model = args.get("model") || process.env.DOCS_LLM_MODEL || "llama3.1:8b";
const codexModel = args.get("codex-model") || process.env.DOCS_CODEX_MODEL || "gpt-5.3-codex";
const baseUrl = (args.get("base-url") || process.env.DOCS_LLM_BASE_URL || "http://127.0.0.1:11434").replace(/\/$/, "");
const numCtx = Number(args.get("num-ctx") || process.env.DOCS_LLM_NUM_CTX || 32768);
const keepAlive = args.get("keep-alive") || process.env.DOCS_LLM_KEEP_ALIVE || "10m";
const codexCommand = args.get("codex-command") || process.env.DOCS_CODEX_COMMAND || (process.platform === "win32" ? "codex.cmd" : "codex");

const schema = {
  type: "object",
  additionalProperties: false,
  required: ["targetPath", "title", "frontMatter", "markdown", "sourceFiles", "warnings"],
  properties: {
    targetPath: {
      type: "string",
      description: "Suggested repository-relative path for the final docs page.",
    },
    title: { type: "string" },
    frontMatter: {
      type: "object",
      additionalProperties: false,
      required: ["sidebar_position", "title"],
      properties: {
        sidebar_position: { type: "number" },
        title: { type: "string" },
      },
    },
    markdown: {
      type: "string",
      description: "Complete Markdown body excluding front matter.",
    },
    sourceFiles: {
      type: "array",
      items: { type: "string" },
      description: "Repository-relative files that informed the draft.",
    },
    warnings: {
      type: "array",
      items: { type: "string" },
      description: "Uncertainties or follow-up checks needed before publishing.",
    },
  },
};

const schemaText = JSON.stringify(schema, null, 2);
const criticalFacts = [
  "upbrr is not a download manager.",
  "upbrr prepares and submits uploads for private-tracker workflows.",
  "The CLI command accepts release source paths and flags; it does not have download/list subcommands.",
  "Safe modes include --dry-run and --site-check.",
  "Queue processing uses --queue and --limit-queue.",
  "Upload-only processing uses --upload-only.",
  "GUI mode can be launched with --gui, and embedded web mode uses the serve command.",
  "The license is GPL-2.0-or-later.",
];
const prompt = [
  "You draft documentation for upbrr, a Go upload preparation and tracker submission tool.",
  "Use the provided repository context only. Do not invent flags, config keys, or behavior.",
  "Return only a single JSON object matching the schema below. Do not wrap it in Markdown fences.",
  "Keep the style direct, concise, and suitable for Docusaurus Markdown.",
  "Do not include secrets or private tracker-sensitive data.",
  "Do not write generic project templates. Do not use placeholders such as Project Name, yourusername, requirements.txt, or TODO.",
  "The project is upbrr, the repository is autobrr/upbrr, and the license is GPL-2.0-or-later.",
  "For this repository, documentation drafts should target documentation/docs unless explicitly asked otherwise.",
  "Critical facts that must be followed:",
  ...criticalFacts.map((fact) => `- ${fact}`),
  `Requested topic: ${topic}`,
  `Preferred target: ${target}`,
  "",
  "Required JSON schema:",
  schemaText,
  "",
  "Repository context JSON:",
  JSON.stringify(context, null, 2),
].join("\n");

async function generateWithOpenAICompatible() {
  const response = await fetch(`${baseUrl}/v1/chat/completions`, {
    method: "POST",
    headers: {
      "Content-Type": "application/json",
    },
    body: JSON.stringify({
      model,
      messages: [
        {
          role: "system",
          content:
            "You are a technical documentation drafter. Return valid JSON only and follow the requested schema exactly.",
        },
        {
          role: "user",
          content: prompt,
        },
      ],
      temperature: 0,
      seed: 1,
      response_format: {
        type: "json_object",
      },
    }),
  });

  if (!response.ok) {
    throw new Error(`${response.status} ${response.statusText}: ${await response.text()}`);
  }

  const payload = await response.json();
  return payload.choices?.[0]?.message?.content;
}

async function generateWithOllamaNative() {
  const response = await fetch(`${baseUrl}/api/chat`, {
    method: "POST",
    headers: {
      "Content-Type": "application/json",
    },
    body: JSON.stringify({
      model,
      messages: [
        {
          role: "system",
          content:
            "You are a technical documentation drafter. Return valid JSON only. Use only provided repository context.",
        },
        {
          role: "user",
          content: prompt,
        },
      ],
      stream: false,
      format: schema,
      keep_alive: keepAlive,
      options: {
        temperature: 0,
        seed: 1,
        num_ctx: Number.isFinite(numCtx) && numCtx > 0 ? numCtx : 32768,
      },
    }),
  });

  if (!response.ok) {
    throw new Error(`${response.status} ${response.statusText}: ${await response.text()}`);
  }

  const payload = await response.json();
  return payload.message?.content;
}

function generateWithCodex() {
  const outputPath = path.join(draftsDir, `codex-${Date.now()}.json`);
  mkdirSync(draftsDir, { recursive: true });

  const codexPrompt = [
    prompt,
    "",
    "Important execution rules:",
    "- Return only the JSON object matching the schema.",
    "- Do not edit files.",
    "- Do not run shell commands.",
    "- Use the repository context included in this prompt as the primary source of truth.",
  ].join("\n");

  const codexArgs = [
    "exec",
    "--ephemeral",
    "-C",
    repoRoot,
    "-s",
    "read-only",
    "-m",
    codexModel,
    "--output-schema",
    schemaPath,
    "-o",
    outputPath,
    "-",
  ];
  const command = process.platform === "win32" ? process.env.ComSpec || "cmd.exe" : codexCommand;
  const commandArgs = process.platform === "win32" ? ["/d", "/s", "/c", codexCommand, ...codexArgs] : codexArgs;

  const result = spawnSync(
    command,
    commandArgs,
    {
      cwd: repoRoot,
      encoding: "utf8",
      input: codexPrompt,
      maxBuffer: 1024 * 1024 * 256,
    },
  );

  if (result.status !== 0) {
    const details = [
      result.error ? `spawn error: ${result.error.message}` : "",
      result.signal ? `signal: ${result.signal}` : "",
      result.stdout,
      result.stderr,
    ]
      .filter(Boolean)
      .join("\n")
      .trim();
    throw new Error(details || `codex exited with status ${result.status}`);
  }

  return readFileSync(outputPath, "utf8");
}

function parseDraft(raw) {
  if (!raw || typeof raw !== "string") {
    throw new Error("local LLM response did not contain text");
  }

  const trimmed = raw.trim();
  try {
    return JSON.parse(trimmed);
  } catch {
    const match = trimmed.match(/\{[\s\S]*\}/);
    if (!match) {
      throw new Error("local LLM response did not contain a JSON object");
    }
    return JSON.parse(match[0]);
  }
}

function validateDraft(draft) {
  const required = ["targetPath", "title", "frontMatter", "markdown", "sourceFiles", "warnings"];
  for (const key of required) {
    if (!(key in draft)) {
      throw new Error(`local LLM draft is missing required key: ${key}`);
    }
  }
  if (typeof draft.targetPath !== "string" || typeof draft.title !== "string" || typeof draft.markdown !== "string") {
    throw new Error("local LLM draft has invalid string fields");
  }
  if (!Array.isArray(draft.sourceFiles) || !Array.isArray(draft.warnings)) {
    throw new Error("local LLM draft sourceFiles and warnings must be arrays");
  }
  if (!draft.targetPath.startsWith("documentation/docs/") && !draft.targetPath.startsWith("docs/")) {
    throw new Error(`local LLM draft targetPath must be a docs page, got: ${draft.targetPath}`);
  }
  const combined = `${draft.title}\n${JSON.stringify(draft.frontMatter)}\n${draft.markdown}`;
  const bannedPatterns = [
    /\bProject Name\b/i,
    /\byourusername\b/i,
    /\brequirements\.txt\b/i,
    /\bMIT License\b/i,
    /\bupbrr\s+download\b/i,
    /\bupbrr\s+list\b/i,
    /\bcontent_id\b/i,
    /\b--auth-token\b/i,
    /\bdownload(s|ing)? content\b/i,
    /\bdownloading, parsing\b/i,
    /\bDownloadMetadata\b/i,
    /\bParseContent\b/i,
    /\bUpdateLocalCache\b/i,
    /\bservice stubs\b/i,
    /\bimplementation steps\b/i,
    /\binternal\/cli\/upbrr\.go\b/i,
    /\[Describe\b/i,
    /\[Prerequisite\b/i,
    /\bTODO\b/i,
  ];
  for (const pattern of bannedPatterns) {
    if (pattern.test(combined)) {
      throw new Error(`local LLM draft looks like a generic template: matched ${pattern}`);
    }
  }
  if (!/\bupbrr\b/i.test(combined)) {
    throw new Error("local LLM draft does not mention upbrr");
  }
  return draft;
}

let outputText;
try {
  if (provider === "codex") {
    outputText = generateWithCodex();
  } else if (provider === "ollama") {
    outputText = await generateWithOllamaNative();
  } else {
    outputText = await generateWithOpenAICompatible();
  }
} catch (error) {
  console.error(
    provider === "codex"
      ? `Codex docs generation failed with model ${codexModel}`
      : `Local LLM request failed via ${provider} at ${baseUrl}`,
  );
  console.error(error instanceof Error ? error.message : String(error));
  console.error("Set DOCS_CODEX_MODEL for Codex, or pass --provider ollama/openai-compatible for local LLMs.");
  process.exit(1);
}

let draft;
try {
  draft = validateDraft(parseDraft(outputText));
} catch (error) {
  console.error(error instanceof Error ? error.message : String(error));
  console.error("Raw local LLM output:");
  console.error(outputText);
  process.exit(1);
}

const stamp = new Date().toISOString().replace(/[:.]/g, "-");
const slug = draft.title
  .toLowerCase()
  .replace(/[^a-z0-9]+/g, "-")
  .replace(/^-|-$/g, "")
  .slice(0, 80);

mkdirSync(draftsDir, { recursive: true });

const jsonPath = path.join(draftsDir, `${stamp}-${slug}.json`);
const mdPath = path.join(draftsDir, `${stamp}-${slug}.md`);
const frontMatter = Object.entries(draft.frontMatter || {})
  .map(([key, value]) => `${key}: ${JSON.stringify(value)}`)
  .join("\n");

writeFileSync(jsonPath, `${JSON.stringify(draft, null, 2)}\n`);
writeFileSync(mdPath, `---\n${frontMatter}\n---\n\n${draft.markdown.trim()}\n`);

console.log(`Wrote ${path.relative(repoRoot, jsonPath)}`);
console.log(`Wrote ${path.relative(repoRoot, mdPath)}`);
console.log("Review the draft manually before copying it into documentation/docs.");
