const fs = require('node:fs');
const { DatabaseSync } = require('node:sqlite');
const { createHash } = require('node:crypto');

// All inputs and the richer output stay in the private run. Never print database rows.
const [databasePath, apiSnapshotPath, outputPath, expectedWorkflowId] = process.argv.slice(2);
const db = new DatabaseSync(databasePath, { readOnly: true });
try {
  const rows = db.prepare("SELECT state_json FROM release_workflow_states WHERE owner_id = 'cli' ORDER BY updated_at DESC").all();
  const states = rows.map(row => JSON.parse(Buffer.from(row.state_json).toString('utf8')));
  const state = states.find(item => item.Composite);
  if (!state) throw new Error('cli_composite_snapshot_missing');
  const api = JSON.parse(fs.readFileSync(apiSnapshotPath, 'utf8'));
  if (!expectedWorkflowId || api.workflow?.id !== expectedWorkflowId) throw new Error('cli_baseline_workflow_mismatch');
  const current = {};
  for (const [field, collection, ref] of [['release', 'Releases', 'release'], ['selection', 'Selections', 'selection'], ['projections', 'Projections', 'trackerProjections'], ['preflight', 'Preflights', 'trackerPreflight'], ['dupes', 'Dupes', 'dupes']]) {
    current[field] = state[collection]?.[state.Workflow[ref]?.id];
  }
  const code = value => typeof value === 'string' && /^[a-z][a-z0-9_]{0,80}$/.test(value) ? value : 'unknown';
  const hash = value => createHash('sha256').update(JSON.stringify(value ?? null)).digest('hex');
  const normalize = snapshot => ({
    prepared: Boolean(snapshot.release),
    trackers: (snapshot.selection?.trackerIds || []).map(id => /^[A-Z0-9]+$/.test(id) ? id : 'UNKNOWN'),
    projections: (snapshot.projections?.projections || []).map(item => ({
      trackerId: /^[A-Z0-9]+$/.test(item.trackerId) ? item.trackerId : 'UNKNOWN',
      reviewedNameHash: hash(item.uploadReleaseName), taxonomyHash: hash(item.taxonomy),
      providerIdentityHash: hash(item.providerIds), duplicateCriteriaHash: hash(item.duplicateCriteria), duplicateTargetHash: hash(item.duplicateTarget),
      duplicatePolicyHash: hash(item.duplicatePolicyId), readiness: code(item.readiness),
      failureCodes: (item.failures || []).map(failure => code(failure.failure?.code)).sort(),
    })).sort((a, b) => a.trackerId.localeCompare(b.trackerId)),
    duplicates: (snapshot.dupes?.results || []).map(item => ({
      trackerId: /^[A-Z0-9]+$/.test(item.trackerId) ? item.trackerId : 'UNKNOWN',
      decision: code(item.decision), status: code(item.status),
      failureCodes: (item.failures || []).map(failure => code(failure.failure?.code)).sort(),
    })).sort((a, b) => a.trackerId.localeCompare(b.trackerId)),
  });
  const baseline = normalize(api);
  const cli = normalize(current);
  const changedFields = Object.keys(baseline).filter(key => JSON.stringify(baseline[key]) !== JSON.stringify(cli[key]));
  const forbiddenEffects = db.prepare("SELECT COUNT(*) AS count FROM release_workflow_effects WHERE kind IN ('tracker_submission', 'client_injection')").get().count;
  const safeIntent = state.Composite.goal === 'dry_run' && state.Composite.intent?.noSeed === true;
  const sufficientEvidence = baseline.prepared && cli.prepared && baseline.projections.length > 0 && cli.projections.length > 0 && baseline.duplicates.length > 0 && cli.duplicates.length > 0;
  const status = !safeIntent || forbiddenEffects !== 0 ? 'fail' : sufficientEvidence && changedFields.length === 0 ? 'pass' : 'inconclusive';
  fs.writeFileSync(outputPath, JSON.stringify({
    version: 1, status, reason: status === 'fail' ? 'cli_safety_contract_failed' : status === 'pass' ? 'shared_decision_observations_match' : 'shared_decision_evidence_requires_review',
    safeIntent, executionMode: code(state.Composite.intent?.executionMode), forbiddenEffects,
    sufficientEvidence, changedFields, baseline, cli,
    limitation: 'Live evidence may change between observations. Name hashes compare equality without disclosing media identity; differing results require review.',
  }, null, 2));
} finally { db.close(); }
