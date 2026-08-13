---
title: Contributing and support
description: Report issues safely, discuss changes, and run repository checks for documentation contributions.
---

# Contributing and support

Use these project channels:

- [GitHub issues](https://github.com/autobrr/upbrr/issues) for reproducible defects and feature proposals;
- [autobrr Discord](https://discord.autobrr.com) for community discussion;
- [GitHub pull requests](https://github.com/autobrr/upbrr/pulls) for reviewed changes.

Discuss large behavior changes before implementation. Read the repository [contribution guide](https://github.com/autobrr/upbrr/blob/main/CONTRIBUTING.md) and applicable `AGENTS.md` files.

## Report a defect

Include:

1. upbrr version and build identifier;
2. operating system and installation method;
3. affected surface: CLI, Web UI, API, tracker, image host, or client;
4. shortest reproduction;
5. expected and actual outcome;
6. sanitized logs.

Use synthetic release data such as `Example.Release.2026.1080p-GRP` and `tt1234567`.

Never include credentials, cookies, passkeys, announce URLs, OTP data, plaintext config exports, private tracker rules, or raw secret-bearing responses.

## Edit this site

Public documentation lives in `documentation/`. Internal planning material under `docs/` is separate.

From the repository root:

```powershell
pnpm --dir documentation install --frozen-lockfile
pnpm --dir documentation run start
```

Before opening a pull request:

```powershell
pnpm --dir documentation run format:check
pnpm --dir documentation run typecheck
pnpm --dir documentation run build
git diff --check
```

Do not commit `documentation/build/`, `documentation/.docusaurus/`, or `documentation/node_modules/`.

## Documentation standards

- Verify commands, flags, defaults, endpoints, and fields against current code or executable output.
- Give each page one reader goal.
- Separate tutorials, task guides, reference, and explanation.
- Keep examples synthetic and safe to share.
- Add recovery guidance where a task can fail.
- Update navigation and related pages when changing a shared contract.

## Code contributions

Follow the checks selected by the nearest `AGENTS.md`. Tracker work must also follow [ADDING_TRACKERS.md](https://github.com/autobrr/upbrr/blob/main/ADDING_TRACKERS.md). Do not weaken tests or policy checks to make a change pass.

upbrr is licensed under GPL-2.0-or-later.
