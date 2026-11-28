#!/usr/bin/env bash
# Copyright (c) 2025-2026, Audionut and the autobrr contributors.
# SPDX-License-Identifier: GPL-2.0-or-later
set -euo pipefail

guard="$(dirname -- "$0")/test-loopback.sh"

bash "$guard" node --input-type=module -e '
  import assert from "node:assert/strict";
  import { createServer } from "node:http";
  for (const host of ["127.0.0.1", "::1"]) {
    const server = createServer((_, response) => response.end("local"));
    await new Promise(resolve => server.listen(0, host, resolve));
    try {
      const address = host === "::1" ? "[::1]" : host;
      const response = await fetch("http://" + address + ":" + server.address().port);
      assert.equal(await response.text(), "local");
    } finally {
      server.close();
    }
  }
'

# Ignore the network error in the child. The guard must still fail and report it.
for probe in \
  'await fetch("http://192.0.2.1").catch(() => {});' \
  'await fetch("http://[2001:db8::1]").catch(() => {});' \
  'await (await import("node:dns/promises")).resolve4("tracker-network.example").catch(() => {});'
do
  status=0
  output=$(bash "$guard" node --input-type=module -e "$probe" 2>&1) || status=$?
  if [[ $status != 1 || $output != *'non-loopback packet(s)'* ]]; then
    printf 'Expected the guard to detect blocked traffic. Exit %s. %s\n' "$status" "$output" >&2
    exit 1
  fi
done

status=0
bash "$guard" bash -c 'exit 42' || status=$?
if [[ $status != 42 ]]; then
  echo "Expected child failure 42, got $status" >&2
  exit 1
fi
