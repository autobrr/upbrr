// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import assert from "node:assert/strict";
import { createServer } from "node:http";
import { spawn } from "node:child_process";
import { once } from "node:events";
import test from "node:test";
import { waitForHTTP } from "./appReadiness.ts";

async function foreignServer(t, status = 200, onRequest = () => {}) {
  let requests = 0;
  const server = createServer((request, response) => {
    requests++;
    onRequest();
    response.writeHead(status);
    response.end();
  });
  server.listen(0, "127.0.0.1");
  await once(server, "listening");
  t.after(() => new Promise((resolve) => server.close(resolve)));
  const address = `127.0.0.1:${server.address().port}`;
  return { address, url: `http://${address}/api/auth/status`, requests: () => requests };
}

const liveChild = () => ({ exitCode: null, signalCode: null });
const marker = (address) =>
  `INFO web: serving web UI on ${address} (browser URL http://${address})\n`;

test("foreign HTTP 200 is not readiness without the child's bind marker", async (t) => {
  const foreign = await foreignServer(t);
  await assert.rejects(
    waitForHTTP(foreign.url, liveChild(), [], [[], []], 20),
    /did not become ready/,
  );
  assert.equal(foreign.requests(), 0);
});

test("wrong address, port-prefix and incomplete line markers are rejected", async (t) => {
  const foreign = await foreignServer(t);
  for (const output of [
    [marker(`127.0.0.2:${new URL(foreign.url).port}`)],
    [marker(`${foreign.address}0`)],
    [`INFO web: serving web UI on ${foreign.address}`],
  ]) {
    await assert.rejects(
      waitForHTTP(foreign.url, liveChild(), output, [output], 20),
      /did not become ready/,
    );
  }
  assert.equal(foreign.requests(), 0);
});

test("owned child bind marker and HTTP 200 are accepted with chunked output", async (t) => {
  const child = spawn(
    process.execPath,
    [
      "-e",
      `
    const server = require('node:http').createServer((q, r) => { r.writeHead(200); r.end(); });
    server.listen(0, '127.0.0.1', () => {
      const address = '127.0.0.1:' + server.address().port;
      process.stdout.write(address + '\\n');
      process.stderr.write('INFO web: serving web UI ');
      setTimeout(() => process.stdout.write('unrelated stdout line\\n'), 5);
      setTimeout(() => process.stderr.write('on ' + address + ' (browser URL http://' + address + ')\\n'), 20);
    });
  `,
    ],
    { stdio: ["ignore", "pipe", "pipe"] },
  );
  t.after(async () => {
    if (child.exitCode === null && child.signalCode === null) {
      child.kill();
      await once(child, "exit");
    }
  });
  const output = [];
  const streams = [[], []];
  [child.stdout, child.stderr].forEach((stream, index) => {
    stream.on("data", (chunk) => {
      output.push(String(chunk));
      streams[index].push(String(chunk));
    });
  });
  const [addressChunk] = await once(child.stdout, "data");
  const address = String(addressChunk).split("\n")[0];
  await waitForHTTP(`http://${address}/api/auth/status`, child, output, streams, 2000);
});

test("matching bind marker does not bypass non-200 HTTP readiness", async (t) => {
  const foreign = await foreignServer(t, 503);
  await assert.rejects(
    waitForHTTP(
      foreign.url,
      liveChild(),
      [marker(foreign.address)],
      [[marker(foreign.address)]],
      20,
    ),
    /did not become ready/,
  );
  assert.equal(foreign.requests(), 1);
});

test("child exit rejects readiness before a request", async (t) => {
  const foreign = await foreignServer(t);
  for (const child of [
    { exitCode: 1, signalCode: null },
    { exitCode: null, signalCode: "SIGTERM" },
  ]) {
    await assert.rejects(
      waitForHTTP(foreign.url, child, [marker(foreign.address)], [[marker(foreign.address)]], 20),
      /server exited/,
    );
  }
  assert.equal(foreign.requests(), 0);
});

test("child exit during an HTTP probe cannot be accepted as ready", async (t) => {
  const child = liveChild();
  const foreign = await foreignServer(t, 200, () => {
    child.signalCode = "SIGTERM";
  });
  await assert.rejects(
    waitForHTTP(foreign.url, child, [marker(foreign.address)], [[marker(foreign.address)]], 20),
    /server exited/,
  );
  assert.equal(foreign.requests(), 1);
});

test("stream interleaving neither destroys nor fabricates a bind marker", async (t) => {
  const foreign = await foreignServer(t);
  const fragments = ["INFO web: serving web UI ", `on ${foreign.address}\n`];
  const unrelated = "unrelated output\n";
  await waitForHTTP(
    foreign.url,
    liveChild(),
    [fragments[0], unrelated, fragments[1]],
    [[unrelated], fragments],
    20,
  );
  const streams = [[fragments[0]], [fragments[1]]];
  await assert.rejects(
    waitForHTTP(foreign.url, liveChild(), streams.flat(), streams, 20),
    /did not become ready/,
  );
  assert.equal(foreign.requests(), 1);
});
