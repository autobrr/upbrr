const path = require('node:path');
const os = require('node:os');
const { defineConfig } = require('../../../webui/node_modules/@playwright/test');
const inputOnly = (process.env.UPBRR_LIVE_BROWSER_PHASE || '').startsWith('input');

module.exports = defineConfig({
  testDir: __dirname,
  testMatch: ['live.spec.cjs', 'input.spec.cjs'],
  workers: 1,
  retries: 0,
  timeout: 540_000,
  expect: { timeout: 15_000 },
  outputDir: path.join(process.env.UPBRR_LIVE_RUN_DIR || path.join(os.tmpdir(), 'upbrr-live-discovery'), 'browser-artifacts', ['hosted', 'restart'].includes(process.env.UPBRR_LIVE_BROWSER_PHASE) ? process.env.UPBRR_LIVE_BROWSER_PHASE : 'local'),
  reporter: [['line']],
  use: { browserName: 'chromium', headless: true, screenshot: inputOnly ? 'off' : 'only-on-failure', trace: inputOnly ? 'off' : 'retain-on-failure', video: 'off' },
});
