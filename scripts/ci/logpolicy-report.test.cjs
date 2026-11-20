const assert = require('node:assert/strict');
const test = require('node:test');
const childProcess = require('node:child_process');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const { analyze, bodyFor, resolve, publish } = require('./logpolicy-report.cjs');

const head = 'a'.repeat(40);
const base = 'b'.repeat(40);
const target = { number: 12, runId: 10, attempt: 1, headRepoId: 99, head, base };
const counts = { TRACE: 1, DEBUG: 2, INFO: 3, WARN: 0, ERROR: 1 };
const bot = { id: 41898282, type: 'Bot' };

function harness() {
  const run = {
    id: 10, workflow_id: 5, path: '.github/workflows/logpolicy.yml', event: 'pull_request',
    run_attempt: 1, head_sha: head, head_branch: 'feature', head_repository: { id: 99 },
    status: 'completed', conclusion: 'failure', repository: { id: 7 }, pull_requests: [],
  };
  const pr = { number: 12, state: 'open', head: { sha: head, ref: 'feature', repo: { id: 99 } }, base: { sha: base, repo: { id: 7 } } };
  const context = { repo: { owner: 'example', repo: 'project' }, payload: { workflow_run: { ...run }, repository: { id: 7 } } };
  const comments = [];
  const writes = [];
  const github = {
    rest: {
      actions: {
        getWorkflowRun: async () => ({ data: run }),
        getWorkflow: async () => ({ data: { id: 5 } }),
      },
      pulls: {
        list: Symbol('pulls'),
        get: async () => ({ data: pr }),
      },
      issues: {
        listComments: Symbol('comments'),
        createComment: async args => {
          writes.push(args);
          const saved = { id: comments.length + 1, user: bot, body: args.body };
          comments.push(saved);
          return { data: saved };
        },
        updateComment: async args => {
          writes.push(args);
          const saved = comments.find(comment => comment.id === args.comment_id);
          saved.body = args.body;
          return { data: saved };
        },
      },
    },
    paginate: async method => method === github.rest.pulls.list ? [pr] : comments,
  };
  return { github, context, comments, writes, pr, run };
}

test('fork resolution binds source workflow, head repository and revision even after policy failure', async () => {
  const h = harness();
  assert.deepEqual(await resolve(h.github, h.context), [target]);
  h.pr.head.repo.id = 100;
  assert.deepEqual(await resolve(h.github, h.context), []);
  h.pr.head.repo.id = 99;
  h.run.run_attempt = 2;
  assert.deepEqual(await resolve(h.github, h.context), []);
  h.run.path = '.github/workflows/impostor.yml';
  await assert.rejects(resolve(h.github, h.context), /Unexpected source workflow/);
});

test('one owned comment is reused on reruns and cleared on zero or analysis failure', async () => {
  const h = harness();
  const report = { ...target, status: 'ok', counts };
  // Marker copies and unrelated bot comments must remain untouched.
  h.comments.push({ id: 90, user: { id: 1, type: 'User' }, body: bodyFor(report) });
  h.comments.push({ id: 91, user: bot, body: 'Another CI report' });
  await publish(h.github, h.context, [report]);
  assert.equal(h.writes.length, 1);
  assert.equal(h.comments.length, 3);
  assert.match(h.comments[2].body, /\| \*\*Total\*\* \| \*\*7\*\* \|/);
  assert.match(h.comments[2].body, /\| WARN \| 0 \|/);
  await publish(h.github, h.context, [report]);
  assert.equal(h.writes.length, 1, 'identical reruns need no write');
  h.run.run_attempt = h.context.payload.workflow_run.run_attempt = 2;
  const zero = Object.fromEntries(Object.keys(counts).map(level => [level, 0]));
  await publish(h.github, h.context, [{ ...target, attempt: 2, status: 'ok', counts: zero }]);
  assert.equal(h.comments.length, 3);
  assert.match(h.comments[2].body, /\| \*\*Total\*\* \| \*\*0\*\* \|/);
  await publish(h.github, h.context, [{ ...target, attempt: 2, status: 'failed' }]);
  assert.match(h.comments[2].body, /Analysis unavailable/);
  assert.doesNotMatch(h.comments[2].body, /\| INFO/);
  assert.equal(h.comments[0].body, bodyFor(report));
  assert.equal(h.comments[1].body, 'Another CI report');
});

test('stale heads, bases and attempts cannot replace a newer report', async () => {
  for (const change of ['head', 'base', 'attempt', 'watermark']) {
    const h = harness();
    h.comments.push({ id: 1, user: bot, body: bodyFor({ ...target, runId: 11, status: 'ok', counts }) });
    if (change !== 'watermark') h.comments[0].body = bodyFor({ ...target, status: 'ok', counts });
    if (change === 'head') h.pr.head.sha = 'c'.repeat(40);
    if (change === 'base') h.pr.base.sha = 'c'.repeat(40);
    if (change === 'attempt') h.run.run_attempt = 2;
    await publish(h.github, h.context, [{ ...target, status: 'failed' }]);
    assert.equal(h.writes.length, 0, change);
  }
});

test('a head changing during publication is marked outdated, not current counts', async () => {
  const h = harness();
  const create = h.github.rest.issues.createComment;
  h.github.rest.issues.createComment = async args => {
    const saved = await create(args);
    h.pr.head.sha = 'c'.repeat(40);
    return saved;
  };
  await publish(h.github, h.context, [{ ...target, status: 'ok', counts }]);
  assert.equal(h.comments.length, 1);
  assert.match(h.comments[0].body, /is outdated/);
  assert.doesNotMatch(h.comments[0].body, /\| INFO/);
});

test('malformed reports and uncertain writes fail visibly without retrying', async () => {
  const h = harness();
  for (const report of [
    { ...target, status: 'ok', counts: { ...counts, INFO: -1 } },
    { ...target, status: 'ok', counts: { ...counts, injection: 'untrusted Markdown' } },
    { ...target, status: 'failed', counts },
    { ...target, runId: 9, status: 'ok', counts },
  ]) {
    await assert.rejects(publish(h.github, h.context, [report]));
  }
  assert.equal(h.writes.length, 0);
  let calls = 0;
  h.github.rest.issues.createComment = async () => { calls++; throw new Error('unknown write outcome'); };
  await assert.rejects(publish(h.github, h.context, [{ ...target, status: 'ok', counts }]), /unknown write/);
  assert.equal(calls, 1);
});


test('analysis failures retain safe stage/context diagnostics without leaking subprocess output', t => {
  const temporary = fs.mkdtempSync(path.join(os.tmpdir(), 'logpolicy-failures-'));
  t.after(() => fs.rmSync(temporary, { recursive: true, force: true }));
  const messages = [];
  t.mock.method(console, 'error', message => messages.push(message));
  for (const failure of [
    { at: 'fetch', error: { status: 128, stderr: 'private_fixture_marker' }, expected: 'stage=fetch category=command-exit' },
    { at: 'analyzer', error: { code: 'ETIMEDOUT', stderr: 'private_fixture_marker' }, expected: 'stage=analyze category=timeout' },
    { at: 'analyzer', error: { status: 1, stderr: 'logging count: category=type os=windows tags=e2e count=2\n' }, expected: 'analyzer=type os=windows tags=e2e count=2' },
    { at: 'analyzer', error: { status: 1, stderr: 'logging count: category=type os=windows tags=e2e count=2\nprivate_fixture_marker' }, expected: 'stage=analyze category=command-exit.' },
    { at: 'json', expected: 'stage=validate category=invalid-result' },
  ]) {
    const execute = t.mock.method(childProcess, 'execFileSync', (program, args) => {
      if (program === 'git') {
        if (args[2] === failure.at) throw Object.assign(new Error('private_fixture_marker'), failure.error);
        return args[2] === 'rev-parse' ? head : '';
      }
      if (failure.at === 'analyzer') throw Object.assign(new Error('private_fixture_marker'), failure.error);
      return 'private_fixture_marker';
    });
    assert.deepEqual(analyze([target], 'synthetic-analyzer', 'example/project', temporary), [{ ...target, status: 'failed' }]);
    assert(messages.at(-1).includes(failure.expected), messages.at(-1));
    execute.mock.restore();
  }
  assert(messages.every(message => !message.includes('private_fixture_marker')));
});
