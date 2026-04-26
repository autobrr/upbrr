import { spawnSync } from "node:child_process";
import { mkdirSync, readFileSync } from "node:fs";
import path from "node:path";
import Ajv from "ajv";
import {
  applyDraft,
  contextPath,
  docsRoot,
  draftSchemaPath,
  draftSlug,
  draftsDir,
  formatAjvErrors,
  parseArgs,
  readJsonFile,
  repoRoot,
  validateDraftSemantics,
  writeDraftOutputs,
} from "./doc-tooling.mjs";

const args = parseArgs();

let context;
try {
  context = readJsonFile(contextPath);
} catch {
  console.error(
    "Docs context is missing. Run `pnpm run docs:context` before generating drafts.",
  );
  process.exit(1);
}

const draftSchema = readJsonFile(draftSchemaPath);
const schemaText = JSON.stringify(draftSchema, null, 2);
const ajv = new Ajv({ allErrors: true, strict: false });
const validateDraftSchema = ajv.compile(draftSchema);

const topic =
  args.get("topic") || "improve the most obviously incomplete upbrr docs page";
const legacyDefaultTarget = "documentation/docs/maintenance/generated-draft.md";
const explicitTarget = args.get("target");
const defaultTargetSlug = draftSlug(topic.replace(/^document\s+/i, ""));
const target =
  explicitTarget || `documentation/docs/maintenance/${defaultTargetSlug}.md`;
const shouldApply = args.get("apply") === "true";
const forceApply = args.get("force") === "true";
const allowWarnings = args.get("allow-warnings") === "true";
const provider = (
  args.get("provider") ||
  process.env.DOCS_LLM_PROVIDER ||
  "codex"
).toLowerCase();
const model = args.get("model") || process.env.DOCS_LLM_MODEL || "llama3.1:8b";
const codexModel =
  args.get("codex-model") || process.env.DOCS_CODEX_MODEL || "gpt-5.3-codex";
const baseUrl = (
  args.get("base-url") ||
  process.env.DOCS_LLM_BASE_URL ||
  "http://127.0.0.1:11434"
).replace(/\/$/, "");
const numCtx = Number(
  args.get("num-ctx") || process.env.DOCS_LLM_NUM_CTX || 32768,
);
const keepAlive =
  args.get("keep-alive") || process.env.DOCS_LLM_KEEP_ALIVE || "10m";
const codexCommand =
  args.get("codex-command") ||
  process.env.DOCS_CODEX_COMMAND ||
  (process.platform === "win32" ? "codex.cmd" : "codex");

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
    throw new Error(
      `${response.status} ${response.statusText}: ${await response.text()}`,
    );
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
      format: draftSchema,
      keep_alive: keepAlive,
      options: {
        temperature: 0,
        seed: 1,
        num_ctx: Number.isFinite(numCtx) && numCtx > 0 ? numCtx : 32768,
      },
    }),
  });

  if (!response.ok) {
    throw new Error(
      `${response.status} ${response.statusText}: ${await response.text()}`,
    );
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
    draftSchemaPath,
    "-o",
    outputPath,
    "-",
  ];
  const command =
    process.platform === "win32"
      ? process.env.ComSpec || "cmd.exe"
      : codexCommand;
  const commandArgs =
    process.platform === "win32"
      ? ["/d", "/s", "/c", codexCommand, ...codexArgs]
      : codexArgs;

  const result = spawnSync(command, commandArgs, {
    cwd: repoRoot,
    encoding: "utf8",
    input: codexPrompt,
    maxBuffer: 1024 * 1024 * 256,
  });

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
    throw new Error("LLM response did not contain text");
  }

  const trimmed = raw.trim();
  try {
    return JSON.parse(trimmed);
  } catch {
    const match = trimmed.match(/\{[\s\S]*\}/);
    if (!match) {
      throw new Error("LLM response did not contain a JSON object");
    }
    return JSON.parse(match[0]);
  }
}

function validateDraft(draft) {
  if (!validateDraftSchema(draft)) {
    throw new Error(
      `generated draft failed schema validation: ${formatAjvErrors(validateDraftSchema.errors)}`,
    );
  }
  return validateDraftSemantics(draft);
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
  console.error(
    "Set DOCS_CODEX_MODEL for Codex, or pass --provider ollama/openai-compatible for local LLMs.",
  );
  process.exit(1);
}

let draft;
try {
  const parsedDraft = parseDraft(outputText);
  if (explicitTarget || parsedDraft.targetPath === legacyDefaultTarget) {
    parsedDraft.targetPath = target;
  }
  draft = validateDraft(parsedDraft);
} catch (error) {
  console.error(error instanceof Error ? error.message : String(error));
  console.error("Raw LLM output:");
  console.error(outputText);
  process.exit(1);
}

try {
  const outputs = writeDraftOutputs(draft);
  console.log(`Wrote ${path.relative(repoRoot, outputs.jsonPath)}`);
  console.log(`Wrote ${path.relative(repoRoot, outputs.mdPath)}`);

  if (shouldApply) {
    const target = applyDraft(draft, {
      docsRoot,
      force: forceApply,
      allowWarnings,
    });
    console.log(`Applied draft to ${target.repoRelativePath}`);
  } else {
    console.log(
      "Review the draft manually, or rerun with --apply to publish it into documentation/docs.",
    );
  }
} catch (error) {
  console.error(error instanceof Error ? error.message : String(error));
  process.exit(1);
}
