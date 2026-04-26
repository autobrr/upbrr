---
sidebar_position: 2
title: Import and Export
---

# Import and export

`upbrr` can import YAML, JSON, and legacy Upload Assistant `config.py` files into SQLite-backed configuration.

## Import from CLI

```bash
go run ./cmd/upbrr --import-config path/to/config.yaml
```

Supported extensions include:

- `.yaml`
- `.yml`
- `.json`
- `.py` for legacy Upload Assistant config files

The GUI and web UI can import the same formats from the Settings page.

## Export from CLI

```bash
go run ./cmd/upbrr --export-config path/to/config.yaml
```

Authenticated GUI and web Settings exports do not expose plaintext secret export by default. For a local trusted setup, the hidden `allow_unencrypted_export` flag can be added to the `web-auth.json` file stored beside the active database.

## Environment overrides

Environment overrides can apply at runtime without persisting those values back into the database.
