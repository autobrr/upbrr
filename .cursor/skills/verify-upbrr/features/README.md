# upbrr feature map

One file per user-facing feature. Each answers: what it is, how a user reaches it, how to drive it
with the harnesses in `../SKILL.md`, and what observable end state proves it works.

| Feature | File | Primary surface | Harness |
| --- | --- | --- | --- |
| Tracker rules (SAM naming, structure, language, duplicate slots) | [tracker-rules.md](tracker-rules.md) | CLI + Web UI | focused Go tests, `cli-full-upload`, `site-check` |
| CLI upload pipeline | [cli-upload.md](cli-upload.md) | CLI | `cli-full-upload` Playwright project |
| Embedded Web UI release workflow | [web-workflow.md](web-workflow.md) | Web UI | `web-full-upload` / `web-smoke` Playwright projects |
| Public documentation | [public-docs.md](public-docs.md) | Docusaurus site | `pnpm --dir documentation run check` |

A proof that drives one convenient entry point is incomplete when the feature map lists more. Pick
the surface the change actually touches.
