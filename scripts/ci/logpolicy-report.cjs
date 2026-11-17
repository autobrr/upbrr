const assert = require('node:assert/strict');
const { execFileSync } = require('node:child_process');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');

const marker = '<!-- upbrr-log-counts:v1 -->';
const botId = 41898282;
const levels = ['TRACE', 'DEBUG', 'INFO', 'WARN', 'ERROR'];
const sha = /^[a-f0-9]{40}$/;
const stamp = /^<!-- logpolicy-run:(\d+):(\d+):([a-f0-9]{40}):([a-f0-9]{40}) -->$/;

function validateTarget(target) {
  for (const key of ['number', 'runId', 'attempt', 'headRepoId']) {
    assert(Number.isSafeInteger(target[key]) && target[key] > 0, `Invalid ${key}`);
  }
  assert(sha.test(target.head) && sha.test(target.base), 'Invalid report revision');
}

function validateReport(report) {
  validateTarget(report);
  assert(['ok', 'failed'].includes(report.status), 'Invalid analysis outcome');
  if (report.status === 'ok') {
    assert.deepEqual(Object.keys(report.counts).sort(), [...levels].sort());
    for (const level of levels) {
      assert(Number.isSafeInteger(report.counts[level]) && report.counts[level] >= 0 && report.counts[level] <= 10000000, 'Invalid count');
    }
  } else {
    assert(!Object.hasOwn(report, 'counts'), 'Failed analysis cannot contain counts');
  }
}

async function sourceRun(github, context) {
  const { owner, repo } = context.repo;
  const event = context.payload.workflow_run;
  const { data: run } = await github.rest.actions.getWorkflowRun({ owner, repo, run_id: event.id });
  const { data: workflow } = await github.rest.actions.getWorkflow({ owner, repo, workflow_id: 'logpolicy.yml' });
  assert(run.repository.id === context.payload.repository.id && run.workflow_id === workflow.id &&
    run.path === '.github/workflows/logpolicy.yml', 'Unexpected source workflow');
  return run;
}

// Resolve against live metadata, including fork runs with no pull_requests entry.
async function resolve(github, context) {
  const run = await sourceRun(github, context);
  if (run.event !== 'pull_request' || run.status !== 'completed' ||
      run.run_attempt !== context.payload.workflow_run.run_attempt) return [];
  const pulls = await github.paginate(github.rest.pulls.list, { ...context.repo, state: 'open', per_page: 100 });
  return pulls.filter(pr => pr.head.sha === run.head_sha && pr.head.repo?.id === run.head_repository?.id &&
    pr.head.ref === run.head_branch && pr.base.repo.id === context.payload.repository.id).map(pr => {
    const target = {
      number: pr.number,
      runId: run.id,
      attempt: run.run_attempt,
      headRepoId: pr.head.repo.id,
      head: pr.head.sha,
      base: pr.base.sha,
    };
    validateTarget(target);
    return target;
  });
}

function analyze(targets, binary, repository, temporary) {
  assert(/^[A-Za-z0-9_.-]+\/[A-Za-z0-9_.-]+$/.test(repository), 'Invalid repository');
  return targets.map(target => {
    validateTarget(target);
    const root = fs.mkdtempSync(path.join(temporary, 'log-count-source-'));
    const git = args => execFileSync('git', ['-C', root, ...args], { encoding: 'utf8', timeout: 600000, stdio: ['ignore', 'pipe', 'pipe'] });
    try {
      git(['init', '--quiet']);
      git(['remote', 'add', 'origin', `https://github.com/${repository}.git`]);
      git(['fetch', '--quiet', '--no-tags', 'origin', target.head, target.base]);
      git(['checkout', '--quiet', '--detach', target.head]);
      assert.equal(git(['rev-parse', 'HEAD']).trim(), target.head);
      const counts = JSON.parse(execFileSync(binary, [
        '--added-base', target.base, '--added-head', target.head, '--source', root,
      ], {
        encoding: 'utf8', timeout: 600000, maxBuffer: 1024 * 1024,
        env: { ...process.env, CGO_ENABLED: '0', GOTOOLCHAIN: 'local', GOWORK: 'off', GOFLAGS: '', GOPACKAGESDRIVER: 'off' },
        stdio: ['ignore', 'pipe', 'pipe'],
      }));
      const report = { ...target, status: 'ok', counts };
      validateReport(report);
      return report;
    } catch {
      // Never publish arbitrary subprocess output (or fabricated zero counts).
      console.error(`Logging count analysis failed for PR #${target.number}.`);
      return { ...target, status: 'failed' };
    } finally {
      fs.rmSync(root, { recursive: true, force: true });
    }
  });
}

function bodyFor(report) {
  const identity = `<!-- logpolicy-run:${report.runId}:${report.attempt}:${report.base}:${report.head} -->`;
  const header = `${marker}\n${identity}\n### Added logging statements\n\nAnalyzed commit: \`${report.head}\` (base \`${report.base}\`).\n\n`;
  if (report.status !== 'ok') {
    return header + '**Analysis unavailable. No counts were produced.** Check the logging report workflow. Logging-policy validation has its own unchanged check.';
  }
  const rows = levels.map(level => `| ${level} | ${report.counts[level]} |`).join('\n');
  const total = levels.reduce((sum, level) => sum + report.counts[level], 0);
  return header + `Go logging source statements on the added side of the full PR diff; not runtime log volume. Edits count once per affected call, removals do not subtract, and comment-only edits do not count.\n\n| Level | Added |\n| --- | ---: |\n${rows}\n| **Total** | **${total}** |\n\nLogging-policy validation is reported separately.`;
}

function newerComment(body, report) {
  const match = stamp.exec(body.split('\n')[1] ?? '');
  assert(match, 'Owned logging comment has invalid provenance; refusing to overwrite');
  return BigInt(match[1]) > BigInt(report.runId) ||
    (BigInt(match[1]) === BigInt(report.runId) && BigInt(match[2]) > BigInt(report.attempt));
}

async function current(github, context, run, report) {
  const { data: liveRun } = await github.rest.actions.getWorkflowRun({ ...context.repo, run_id: report.runId });
  if (liveRun.run_attempt !== report.attempt || liveRun.status !== 'completed' || liveRun.head_sha !== report.head ||
      liveRun.id !== run.id || liveRun.head_repository?.id !== report.headRepoId) return false;
  const { data: pr } = await github.rest.pulls.get({ ...context.repo, pull_number: report.number });
  return pr.state === 'open' && pr.head.sha === report.head && pr.base.sha === report.base &&
    pr.head.repo?.id === report.headRepoId && pr.base.repo.id === context.payload.repository.id;
}

// Called only by the serialized writer, which never checks out or executes PR code.
async function publish(github, context, reports) {
  assert(Array.isArray(reports) && reports.length <= 1000, 'Invalid report list');
  const run = await sourceRun(github, context);
  assert(run.event === 'pull_request', 'Not a PR policy run');
  for (const report of reports) {
    validateReport(report);
    assert(report.runId === run.id && report.head === run.head_sha && report.attempt === context.payload.workflow_run.run_attempt,
      'Report is not bound to the triggering run');
    const comments = await github.paginate(github.rest.issues.listComments, { ...context.repo, issue_number: report.number, per_page: 100 });
    const owned = comments.find(comment => comment.user?.id === botId && comment.user.type === 'Bot' && comment.body?.startsWith(marker + '\n'));
    if (owned && newerComment(owned.body, report)) continue;
    // GitHub has no atomic PR-head precondition for comment writes. Serialize
    // writers and check both revisions just before writing, then check again.
    if (!await current(github, context, run, report)) continue;
    const body = bodyFor(report);
    if (owned?.body === body) continue;
    const { data: saved } = owned
      ? await github.rest.issues.updateComment({ ...context.repo, comment_id: owned.id, body })
      : await github.rest.issues.createComment({ ...context.repo, issue_number: report.number, body });
    assert(saved.user?.id === botId && saved.user.type === 'Bot' && saved.body === body, 'Comment readback mismatch; do not retry blindly');
    if (!await current(github, context, run, report)) {
      await github.rest.issues.updateComment({ ...context.repo, comment_id: saved.id,
        body: `${marker}\n${body.split('\n')[1]}\nLogging analysis for \`${report.head}\` is outdated. Waiting for the current PR revision; no current counts are claimed.` });
    }
  }
}

module.exports = { analyze, resolve, publish, validateReport, bodyFor };

if (require.main === module) {
  const targets = JSON.parse(process.env.LOGPOLICY_TARGETS);
  const reports = analyze(targets, process.env.LOGPOLICY_BINARY, process.env.GITHUB_REPOSITORY, os.tmpdir());
  fs.appendFileSync(process.env.GITHUB_OUTPUT, `reports=${JSON.stringify(reports)}\n`);
  if (reports.some(report => report.status !== 'ok')) process.exitCode = 1;
}
