const fs = require('node:fs');
const path = require('node:path');
const { execFileSync } = require('node:child_process');
const { createHash } = require('node:crypto');
const { test, expect } = require('../../../webui/node_modules/@playwright/test');
const { createRequestPacer } = require('./helpers.cjs');

const correctionFields = [
  'identity.tmdb', 'identity.imdb', 'identity.tvdb', 'identity.tvmaze', 'identity.mal',
  'release_name.category', 'release_name.type', 'release_name.source', 'release_name.resolution',
  'release_name.tag', 'release_name.service', 'release_name.edition', 'release_name.season',
  'release_name.episode', 'release_name.episode_title', 'release_name.manual_year',
  'release_name.manual_date', 'release_name.use_season_episode', 'release_name.no_season',
  'release_name.no_year', 'release_name.no_aka', 'release_name.no_tag',
  'release_name.no_episode_title', 'release_name.no_distributor', 'release_name.no_edition',
  'release_name.no_dub', 'release_name.no_dual', 'release_name.dual_audio', 'release_name.region',
  'metadata.distributor', 'metadata.original_language', 'metadata.personal_release',
  'metadata.commentary', 'metadata.web_dv', 'metadata.stream_optimized', 'metadata.anime',
  'metadata.title', 'metadata.alternate_title', 'metadata.original_title', 'metadata.genres',
  'metadata.audio_languages', 'metadata.subtitle_languages', 'metadata.hardcoded_subs',
  'metadata.hardcoded_subtitle_languages', 'metadata.track_languages',
];

const booleanFields = new Set([
  'release_name.use_season_episode', 'release_name.no_season', 'release_name.no_year',
  'release_name.no_aka', 'release_name.no_tag', 'release_name.no_episode_title',
  'release_name.no_distributor', 'release_name.no_edition', 'release_name.no_dub',
  'release_name.no_dual', 'release_name.dual_audio', 'metadata.personal_release',
  'metadata.commentary', 'metadata.web_dv', 'metadata.stream_optimized', 'metadata.anime',
  'metadata.hardcoded_subs',
]);

const downstreamMethods = new Set([
  'CheckReleaseWorkflowDuplicates', 'CaptureReleaseWorkflowMedia',
  'GetReleaseWorkflowMediaPlan', 'PreviewReleaseWorkflowFrame', 'StageReleaseWorkflowMedia',
  'AttachReleaseWorkflowMedia', 'SetReleaseWorkflowMediaSelection', 'DeleteReleaseWorkflowMedia',
  'ReorderReleaseWorkflowMedia', 'UploadReleaseWorkflowImages', 'RetryReleaseWorkflowImageHost',
  'RemoveReleaseWorkflowHostedImages', 'GenerateReleaseWorkflowDescriptions',
  'SaveReleaseWorkflowDescriptionOverride', 'ResetReleaseWorkflowDescriptionOverride',
  'DryRunReleaseWorkflowUploads', 'UploadReleaseWorkflow', 'RetryReleaseWorkflowUpload',
  'RetryReleaseWorkflowClientInjection', 'InvalidateReleaseWorkflowTrackers',
  'ListTrackerAuthCapabilities', 'GetTrackerAuthStatus', 'TestTrackerAuth',
  'LoginTrackerAuth', 'SubmitTrackerAuth2FA', 'ImportTrackerAuthCookieContent',
  'DeleteTrackerAuth',
]);

const hash = value => createHash('sha256').update(JSON.stringify(value ?? null)).digest('hex');
const correctionRevision = current => Number(current.corrections?.revision ?? current.inputReadiness?.correctionRevision ?? 0);
const refKey = ref => `${ref.field}\u0000${ref.trackId || ''}`;
const canonicalTrackerIds = trackerIds => Array.isArray(trackerIds) && trackerIds.every(trackerId => typeof trackerId === 'string')
  ? [...trackerIds].sort()
  : null;

function selectedTrackerIdentityMatches(actual, expected) {
  const actualIds = canonicalTrackerIds(actual);
  const expectedIds = canonicalTrackerIds(expected);
  return actualIds !== null && expectedIds !== null && JSON.stringify(actualIds) === JSON.stringify(expectedIds);
}

function correctionPatchRequest(requests, startIndex) {
  const requestWindow = requests.slice(startIndex);
  if (requestWindow.length === 0) throw new Error('input_correction_request_missing');
  if (requestWindow.some(request => request.goal !== 'input_ready')) throw new Error('input_correction_crossed_goal_boundary');
  const patched = requestWindow.filter(request => request.intent?.correctionPatch);
  if (patched.length === 0) throw new Error('input_correction_patch_missing');
  if (patched.length > 1) throw new Error('input_correction_patch_duplicated');
  return patched[0];
}

function assertInputBoundary(current, trackerIds) {
  expect(current.release, 'prepared_release_required').toBeTruthy();
  expect(current.inputReadiness, 'input_readiness_required').toBeTruthy();
  expect(current.workflow.inputReadiness?.id).toBe(current.inputReadiness.id);
  expect(selectedTrackerIdentityMatches(current.inputReadiness.selectedTrackerIds, trackerIds), 'selected_tracker_identity_changed').toBe(true);
  for (const field of ['projections', 'preflight', 'dupes', 'media', 'descriptions', 'dryRun', 'uploadResult']) {
    expect(current[field], `excluded_current_stage_${field}`).toBeFalsy();
  }
  for (const field of ['trackerProjections', 'trackerPreflight', 'dupes', 'media', 'descriptions', 'dryRun', 'uploadResult']) {
    expect(current.workflow[field], `excluded_workflow_ref_${field}`).toBeFalsy();
  }
}

test('Input tracker identity ignores canonical order and rejects membership changes', () => {
  const expected = ['LST', 'AITHER', 'BTN'];
  expect(selectedTrackerIdentityMatches(['AITHER', 'BTN', 'LST'], expected)).toBe(true);
  expect(selectedTrackerIdentityMatches(['AITHER', 'LST'], expected), 'missing_tracker_accepted').toBe(false);
  expect(selectedTrackerIdentityMatches(['AITHER', 'BTN', 'LST', 'PTP'], expected), 'extra_tracker_accepted').toBe(false);
  expect(selectedTrackerIdentityMatches(['AITHER', 'BTN', 'BTN', 'LST'], expected), 'duplicate_tracker_accepted').toBe(false);
});

test('Input correction request windows retain the accepted patch', () => {
  const previous = { goal: 'input_ready', intent: { correctionPatch: { expectedRevision: 3 } } };
  const patched = { goal: 'input_ready', intent: { correctionPatch: { expectedRevision: 4 } } };
  const followUp = { goal: 'input_ready', intent: {} };
  expect(correctionPatchRequest([previous, patched, followUp], 1)).toBe(patched);
  expect(() => correctionPatchRequest([followUp], 0)).toThrow('input_correction_patch_missing');
  expect(() => correctionPatchRequest([patched, followUp, patched], 0)).toThrow('input_correction_patch_duplicated');
  expect(() => correctionPatchRequest([patched, { goal: 'prepared', intent: {} }], 0)).toThrow('input_correction_crossed_goal_boundary');
});

const identityKeys = {
  'identity.tmdb': 'TMDBID', 'identity.imdb': 'IMDBID', 'identity.tvdb': 'TVDBID',
  'identity.tvmaze': 'TVmazeID', 'identity.mal': 'MALID',
};
const releaseKeys = {
  'release_name.category': 'Category', 'release_name.type': 'Type',
  'release_name.source': 'Source', 'release_name.resolution': 'Resolution',
  'release_name.tag': 'Tag', 'release_name.service': 'Service',
  'release_name.edition': 'Edition', 'release_name.season': 'Season',
  'release_name.episode': 'Episode', 'release_name.episode_title': 'EpisodeTitle',
  'release_name.manual_year': 'ManualYear', 'release_name.manual_date': 'ManualDate',
  'release_name.use_season_episode': 'UseSeasonEpisode', 'release_name.no_season': 'NoSeason',
  'release_name.no_year': 'NoYear', 'release_name.no_aka': 'NoAKA',
  'release_name.no_tag': 'NoTag', 'release_name.no_episode_title': 'NoEpisodeTitle',
  'release_name.no_distributor': 'NoDistributor', 'release_name.no_edition': 'NoEdition',
  'release_name.no_dub': 'NoDub', 'release_name.no_dual': 'NoDual',
  'release_name.dual_audio': 'DualAudio',
  'release_name.region': 'Region',
};
const metadataKeys = {
  'metadata.distributor': 'Distributor', 'metadata.original_language': 'OriginalLanguage',
  'metadata.personal_release': 'PersonalRelease', 'metadata.commentary': 'Commentary',
  'metadata.web_dv': 'WebDV', 'metadata.stream_optimized': 'StreamOptimized',
  'metadata.anime': 'Anime',
  'metadata.title': 'Title', 'metadata.alternate_title': 'AlternateTitle',
  'metadata.original_title': 'OriginalTitle', 'metadata.genres': 'Genres',
  'metadata.audio_languages': 'AudioLanguages', 'metadata.subtitle_languages': 'SubtitleLanguages',
  'metadata.hardcoded_subs': 'HardcodedSubs',
  'metadata.hardcoded_subtitle_languages': 'HardcodedSubtitleLanguages',
};
const hasExplicitCorrection = (values, property) => Object.prototype.hasOwnProperty.call(values || {}, property)
  && values[property] !== null && values[property] !== undefined;

function explicitRefs(current) {
  const corrections = current.corrections?.corrections;
  const refs = new Set();
  if (!corrections) return refs;
  for (const [field, property] of Object.entries(identityKeys)) {
    if (hasExplicitCorrection(corrections.identity, property)) refs.add(refKey({ field }));
  }
  for (const [field, property] of Object.entries(releaseKeys)) {
    if (hasExplicitCorrection(corrections.releaseName, property)) refs.add(refKey({ field }));
  }
  for (const [field, property] of Object.entries(metadataKeys)) {
    if (hasExplicitCorrection(corrections.metadata, property)) refs.add(refKey({ field }));
  }
  for (const correction of corrections.metadata?.TrackLanguages || []) {
    refs.add(refKey({ field: 'metadata.track_languages', trackId: correction.trackId }));
  }
  return refs;
}

const renderValue = value => Array.isArray(value) ? value.join(', ') : value === undefined || value === null ? '' : String(value);

function applicableValue(current, field, trackId) {
  const corrections = current.corrections?.corrections || {};
  if (identityKeys[field] && hasExplicitCorrection(corrections.identity, identityKeys[field])) {
    return renderValue(corrections.identity[identityKeys[field]]);
  }
  if (releaseKeys[field] && hasExplicitCorrection(corrections.releaseName, releaseKeys[field])) {
    return renderValue(corrections.releaseName[releaseKeys[field]]);
  }
  if (metadataKeys[field] && hasExplicitCorrection(corrections.metadata, metadataKeys[field])) {
    return renderValue(corrections.metadata[metadataKeys[field]]);
  }
  const release = current.release?.release || {};
  const identity = release.Identity || {};
  const naming = release.Naming || {};
  const media = release.Media || {};
  const episode = release.Episode || {};
  const derived = {
    'identity.tmdb': identity.TMDBID,
    'identity.imdb': identity.IMDBID,
    'identity.tvdb': identity.TVDBID,
    'identity.tvmaze': identity.TVmazeID,
    'identity.mal': identity.MALID,
    'release_name.category': identity.Category,
    'release_name.type': naming.Type,
    'release_name.source': naming.Source || media.Source,
    'release_name.resolution': naming.Resolution,
    'release_name.tag': naming.Tag || naming.Group,
    'release_name.service': media.Service,
    'release_name.edition': media.Edition || naming.Editions?.[0],
    'release_name.season': episode.SeasonLabel,
    'release_name.episode': episode.EpisodeLabel,
    'release_name.episode_title': episode.Title,
    'release_name.manual_year': naming.Year,
    'release_name.manual_date': episode.DailyDate || episode.AiredDate,
    'release_name.region': naming.Region || media.Region,
    'metadata.distributor': media.Distributor,
    'metadata.original_language': media.OriginalLanguage,
    'metadata.title': naming.Title,
    'metadata.alternate_title': naming.AlternateTitle,
    'metadata.original_title': naming.OriginalTitle,
    'metadata.genres': naming.Genres,
    'metadata.audio_languages': media.AudioLanguages,
    'metadata.subtitle_languages': media.SubtitleLanguages,
    'metadata.hardcoded_subtitle_languages': media.HardcodedSubtitleLanguages,
  }[field];
  if (field === 'metadata.track_languages') {
    const track = (media.Tracks || []).find(item => item.ID === trackId);
    return renderValue(track?.Languages);
  }
  return renderValue(derived);
}

test('Input correction wire nulls remain automatic values', () => {
  const current = {
    corrections: { corrections: {
      identity: { TMDBID: null, IMDBID: 0 },
      releaseName: { Category: null, Source: undefined, UseSeasonEpisode: false, Tag: '' },
      metadata: { Distributor: null, Genres: [] },
    } },
    release: { release: {
      Identity: { TMDBID: 42, Category: 'movie' },
      Naming: { Source: 'WEB', Tag: 'GRP', Genres: ['Drama'] },
      Media: { Distributor: 'Synthetic Studio' },
    } },
  };
  const refs = explicitRefs(current);
  for (const field of ['identity.tmdb', 'release_name.category', 'release_name.source', 'metadata.distributor']) {
    expect(refs.has(refKey({ field })), `null_or_undefined_counted_${field}`).toBe(false);
  }
  for (const field of ['identity.imdb', 'release_name.use_season_episode', 'release_name.tag', 'metadata.genres']) {
    expect(refs.has(refKey({ field })), `explicit_zero_value_lost_${field}`).toBe(true);
  }
  expect(applicableValue(current, 'identity.tmdb')).toBe('42');
  expect(applicableValue(current, 'release_name.category')).toBe('movie');
  expect(applicableValue(current, 'release_name.source')).toBe('WEB');
  expect(applicableValue(current, 'metadata.distributor')).toBe('Synthetic Studio');
  expect(applicableValue(current, 'identity.imdb')).toBe('0');
  expect(applicableValue(current, 'release_name.use_season_episode')).toBe('false');
  expect(applicableValue(current, 'release_name.tag')).toBe('');
  expect(applicableValue(current, 'metadata.genres')).toBe('');
});

async function openInput(page) {
  await page.getByRole('button', { name: 'Input', exact: true }).click();
  const editor = page.getByText('Edit Release Details', { exact: true });
  await expect(editor).toBeVisible();
  const details = editor.locator('xpath=ancestor::details[1]');
  if (!(await details.getAttribute('open'))) await editor.click();
  await expect(page.getByText('Input readiness', { exact: true })).toBeVisible();
}

async function openHistory(page) {
  const source = page.getByLabel('Source path', { exact: true });
  await source.click();
  const history = page.getByRole('listbox', { name: 'Source path history' });
  await expect(history).toBeVisible();
  expect(await history.getByRole('option').count()).toBeGreaterThan(0);
  await page.keyboard.press('Escape');
}

async function lockSkipClientSearch(page) {
  const checkbox = page.getByRole('checkbox', { name: 'Skip client search' });
  await checkbox.check();
  await expect(checkbox).toBeChecked();
}

async function setRowValue(row, current, field, trackId) {
  const select = row.locator('select').first();
  if (await select.count()) {
    const labels = await select.locator('option').allTextContents();
    if (!labels.includes('No')) return { changed: false, reason: 'explicit_false_option_missing' };
    await select.selectOption({ label: 'No' });
    return { changed: true, submitted: false };
  }
  const checkbox = row.locator('input[type="checkbox"]').first();
  if (await checkbox.count()) {
    await checkbox.uncheck();
    return { changed: true, submitted: false };
  }
  const control = row.locator('input:not([type="hidden"]), textarea').first();
  if (!(await control.count())) return { changed: false, reason: 'editable_control_missing' };
  const value = applicableValue(current, field, trackId);
  if (!value || value === '0') return { changed: false, reason: 'applicable_value_unavailable' };
  await control.clear();
  await control.fill(value);
  return { changed: true, submittedHash: hash(value) };
}

test('Input suite retains corrections without downstream effects or media capture', async ({ browser }) => {
  const runDir = process.env.UPBRR_LIVE_RUN_DIR;
  if (!runDir || !path.isAbsolute(runDir)) throw new Error('live_run_directory_required');
  const privateRoot = path.join(process.env.LOCALAPPDATA, 'upbrr-live-testing', 'runs') + path.sep;
  if (!path.resolve(runDir).toLowerCase().startsWith(privateRoot.toLowerCase())) throw new Error('private_run_directory_required');
  const handoff = JSON.parse(fs.readFileSync(path.join(runDir, 'browser.private.json'), 'utf8'));
  test.skip(handoff.suite !== 'Input', 'Only the Input suite uses this lane.');
  if (handoff.baseURL !== 'http://127.0.0.1:7480' || !Number.isInteger(handoff.process.pid) || handoff.imageUploadLimit !== 0 || !handoff.skipRemoteDuplicates) {
    throw new Error('input_handoff_policy_invalid');
  }
  if (fs.existsSync(path.join(runDir, 'cleanup-started'))) throw new Error('cleanup_already_started');
  const processProbe = `$p=Get-Process -Id ${handoff.process.pid} -ErrorAction Stop; $owners=@(Get-NetTCPConnection -State Listen -LocalPort 7480 | Select-Object -ExpandProperty OwningProcess -Unique); @{path=$p.Path;startTicks=$p.StartTime.ToUniversalTime().Ticks;owners=$owners}|ConvertTo-Json -Compress`;
  const processState = JSON.parse(execFileSync('pwsh', ['-NoProfile', '-EncodedCommand', Buffer.from(processProbe, 'utf16le').toString('base64')], { windowsHide: true, encoding: 'utf8' }));
  expect(processState.path).toBe(handoff.process.path);
  expect(processState.startTicks).toBe(handoff.process.startTicks);
  expect(processState.owners).toEqual([handoff.process.pid]);

  const context = await browser.newContext({ baseURL: handoff.baseURL, recordVideo: undefined });
  const paceRequest = createRequestPacer();
  const phase = process.env.UPBRR_LIVE_BROWSER_PHASE || 'input';
  const prefix = phase === 'input-restart' ? 'browser-input-restart' : 'browser-input';
  const requestReceipt = path.join(runDir, `${prefix}-requests.private.json`);
  let requests = 0;
  let excludedRequests = 0;
  const continueRequests = [];
  const consumeRequest = () => {
    if (requests >= handoff.remainingRequests) throw new Error('browser_request_budget_exhausted');
    requests++;
    fs.writeFileSync(requestReceipt, JSON.stringify({ requests }));
  };
  fs.writeFileSync(requestReceipt, JSON.stringify({ requests }));
  await context.route(`${handoff.baseURL}/api/**`, async route => {
    const pathname = new URL(route.request().url()).pathname;
    const method = pathname.split('/').pop();
    if (pathname.startsWith('/api/app/')) consumeRequest();
    if (downstreamMethods.has(method) || pathname.includes('release-workflow-media')) excludedRequests++;
    if (method === 'ContinueReleaseWorkflow') {
      const body = route.request().postDataJSON();
      continueRequests.push(body);
      if (body.goal !== 'input_ready') excludedRequests++;
    }
    await paceRequest();
    await route.continue();
  });
  await context.addCookies(handoff.cookies);
  const page = await context.newPage();
  await paceRequest();
  const authResponse = await context.request.get('/api/auth/status');
  expect(authResponse.ok()).toBe(true);
  const auth = await authResponse.json();
  expect(auth.authenticated).toBe(true);
  const api = async (method, body = {}) => {
    consumeRequest();
    await paceRequest();
    const response = await context.request.post(`/api/app/${method}`, { data: body, headers: { Origin: handoff.baseURL, 'X-CSRF-Token': auth.csrfToken } });
    expect(response.ok(), 'live_api_request_failed').toBe(true);
    return response.json();
  };
  const results = [];
  const fieldLedger = new Map(correctionFields.map(field => [field, { field, status: 'not_applicable', reason: 'field_not_exposed_by_ready_cases', attempts: [] }]));
  const retainedPath = path.join(runDir, 'input-browser-retained.private.json');
  try {
    const info = await api('GetApplicationInfo');
    expect(info.buildIdentifier).toBe(handoff.buildIdentifier);
    expect(info.testRuntime).toMatchObject({ mode: 'live_test', runId: handoff.runId, trackerSubmissionAllowed: false, clientMutationAllowed: false, imageUploadLimit: 0 });
    await page.goto('/');
    await expect(page.getByText('Live testing active', { exact: true })).toBeVisible();
    const lanes = handoff.lanes.filter(lane => lane.workflowId);
    for (const [laneIndex, lane] of lanes.entries()) {
      let current = await api('GetReleaseWorkflow', { workflowId: lane.workflowId });
      assertInputBoundary(current, lane.trackerIds);
      await page.evaluate(workflowId => sessionStorage.setItem('upbrr.activeReleaseWorkflow', workflowId), lane.workflowId);
      await page.reload();
      await expect(page.getByText('Live testing active', { exact: true })).toBeVisible();
      await openInput(page);
      await openHistory(page);
      await expect(page.getByTestId('input-source-options')).toBeVisible();
      const correctionEditor = page.getByTestId('input-correction-editor');
      await expect(correctionEditor).toBeVisible();
      for (const group of ['Provider IDs', 'Release name', 'Metadata and languages', 'Inspected tracks', 'Source options', 'Input readiness']) {
        await expect(correctionEditor.locator('.settings-subgroup__title').filter({ hasText: group })).toHaveText(group);
      }
      for (const trackerInput of await page.locator('[data-tracker-input]').all()) {
        await expect(trackerInput.locator('input, select, textarea').first()).toBeVisible();
      }
      current = await api('GetReleaseWorkflow', { workflowId: lane.workflowId });
      assertInputBoundary(current, lane.trackerIds);
      const sourceInstructions = current.factInstructions?.instructions || {};
      if (sourceInstructions.SourceLookup) {
        await expect(page.getByLabel('Site URL override', { exact: true })).toHaveValue(sourceInstructions.SourceLookup);
      }
      for (const [tracker, sourceID] of Object.entries(sourceInstructions.TrackerIDs || {})) {
        const sourceIDRow = page.locator(`[data-tracker-source-id="${tracker}"]`);
        if (await sourceIDRow.count()) await expect(sourceIDRow.locator('input')).toHaveValue(sourceID);
      }
      const startingCurrent = current;
      const startingExplicit = explicitRefs(current);

      if (phase === 'input-restart') {
        expect(fs.existsSync(retainedPath), 'retained_input_restart_receipt_missing').toBe(true);
        const retained = JSON.parse(fs.readFileSync(retainedPath, 'utf8'));
        expect(lane.laneId).toBe(retained.laneId);
        expect(correctionRevision(current)).toBe(retained.correctionRevision);
        expect(hash(current.release.release)).toBe(retained.effectiveFactsHash);
        for (const ref of retained.refs) expect(explicitRefs(current).has(refKey(ref))).toBe(true);
        const row = page.locator(`[data-correction-field="${retained.commentary.field}"]`).first();
        await expect(row).toBeVisible();
        await expect(row.locator('select option:checked')).toHaveText('No');
        fs.writeFileSync(path.join(runDir, 'snapshots', `${lane.laneId}.private.json`), JSON.stringify(current, null, 2));
        results.push({ caseId: lane.caseId, laneId: lane.laneId, stage: 'restart_input_browser', status: 'pass', reason: 'retained_explicit_false_and_effective_facts_visible', evidence: { correctionRevision: retained.correctionRevision } });
        continue;
      }

      const edited = [];
      const rows = page.locator('[data-correction-field]');
      for (let index = 0; index < await rows.count(); index++) {
        const row = rows.nth(index);
        const field = await row.getAttribute('data-correction-field');
        const trackId = (await row.getAttribute('data-track-id')) || '';
        if (!fieldLedger.has(field) || fieldLedger.get(field).status === 'pass') continue;
        const attempt = await setRowValue(row, current, field, trackId);
        fieldLedger.get(field).attempts.push({ laneId: lane.laneId, trackIdHash: hash(trackId), ...attempt });
        if (attempt.changed) edited.push({ field, trackId });
        else fieldLedger.get(field).reason = attempt.reason;
      }

      if (edited.length > 0) {
        await lockSkipClientSearch(page);
        const priorRevision = correctionRevision(current);
        const requestIndex = continueRequests.length;
        const save = page.getByRole('button', { name: 'Refresh metadata', exact: true });
        await expect(save).toBeEnabled();
        const continued = page.waitForResponse(response => response.url().endsWith('/api/app/ContinueReleaseWorkflow'));
        await save.click();
        await continued;
        await expect(save).toBeEnabled({ timeout: 180_000 });
        expect(continueRequests.length).toBeGreaterThan(requestIndex);
        const submitted = correctionPatchRequest(continueRequests, requestIndex);
        expect(submitted.intent?.correctionPatch?.expectedRevision).toBe(priorRevision);
        current = await api('GetReleaseWorkflow', { workflowId: lane.workflowId });
        assertInputBoundary(current, lane.trackerIds);
        expect(correctionRevision(current)).toBe(priorRevision + 1);
        const present = explicitRefs(current);
        for (const ref of edited) expect(present.has(refKey(ref)), `explicit_field_missing_${ref.field}`).toBe(true);
        const setRevision = correctionRevision(current);
        const persistedHash = hash(current.release.release);
        const provenanceHash = hash({
          identity: current.release.release.Identity?.Provenance,
          naming: {
            title: current.release.release.Naming?.TitleProvenance,
            alternateTitle: current.release.release.Naming?.AlternateTitleProvenance,
            originalTitle: current.release.release.Naming?.OriginalTitleProvenance,
            genres: current.release.release.Naming?.GenresProvenance,
          },
          media: {
            audio: current.release.release.Media?.AudioLanguagesProvenance,
            subtitles: current.release.release.Media?.SubtitleLanguagesProvenance,
            hardcoded: current.release.release.Media?.HardcodedSubsProvenance,
          },
        });
        await page.reload();
        await openInput(page);
        const reloaded = await api('GetReleaseWorkflow', { workflowId: lane.workflowId });
        assertInputBoundary(reloaded, lane.trackerIds);
        expect(hash(reloaded.release.release)).toBe(persistedHash);
        expect(correctionRevision(reloaded)).toBe(correctionRevision(current));

        for (const ref of edited) {
          const selector = `[data-correction-field="${ref.field}"]${ref.trackId ? `[data-track-id="${ref.trackId}"]` : ''}`;
          const row = page.locator(selector);
          if (booleanFields.has(ref.field)) {
            await row.locator('select').selectOption({ label: 'Auto' });
          } else {
            const auto = row.getByRole('button', { name: /^Auto / }).first();
            await expect(auto).toBeVisible();
            await auto.click();
          }
        }
        await lockSkipClientSearch(page);
        const resetRequestIndex = continueRequests.length;
        const resetSave = page.getByRole('button', { name: 'Refresh metadata', exact: true });
        const resetContinued = page.waitForResponse(response => response.url().endsWith('/api/app/ContinueReleaseWorkflow'));
        await resetSave.click();
        await resetContinued;
        await expect(resetSave).toBeEnabled({ timeout: 180_000 });
        expect(continueRequests.length).toBeGreaterThan(resetRequestIndex);
        const reset = correctionPatchRequest(continueRequests, resetRequestIndex);
        expect(reset.intent?.correctionPatch?.expectedRevision).toBe(setRevision);
        expect(reset.intent?.correctionPatch?.resetFields?.map(refKey).sort()).toEqual(edited.map(refKey).sort());
        current = await api('GetReleaseWorkflow', { workflowId: lane.workflowId });
        assertInputBoundary(current, lane.trackerIds);
        expect(correctionRevision(current)).toBe(setRevision + 1);
        const remaining = explicitRefs(current);
        for (const ref of edited) {
          expect(remaining.has(refKey(ref)), `reset_field_retained_${ref.field}`).toBe(false);
          fieldLedger.set(ref.field, {
            ...fieldLedger.get(ref.field), status: 'pass', reason: booleanFields.has(ref.field) ? 'explicit_false_persisted_reloaded_and_reset' : 'value_persisted_reloaded_and_reset',
            laneId: lane.laneId, setRevision, resetRevision: correctionRevision(current),
            generation: reloaded.release.release.Generation, effectiveFactsHash: persistedHash,
            provenanceHash, historyResult: 'reload_match', selectedTrackers: lane.trackerIds,
          });
        }

        if (laneIndex === 0) {
          const commentary = edited.find(ref => ref.field === 'metadata.commentary');
          if (commentary) {
            const retainedRefs = edited.filter(ref => startingExplicit.has(refKey(ref)));
            if (!retainedRefs.some(ref => refKey(ref) === refKey(commentary))) retainedRefs.push(commentary);
            for (const retained of retainedRefs) {
              const retainedRow = page.locator(`[data-correction-field="${retained.field}"]${retained.trackId ? `[data-track-id="${retained.trackId}"]` : ''}`);
              const retainedSet = await setRowValue(retainedRow, startingCurrent, retained.field, retained.trackId);
              expect(retainedSet.changed).toBe(true);
            }
            await lockSkipClientSearch(page);
            const retainedPriorRevision = correctionRevision(current);
            const retainedRequestIndex = continueRequests.length;
            const retainedSave = page.getByRole('button', { name: 'Refresh metadata', exact: true });
            const retainedContinued = page.waitForResponse(response => response.url().endsWith('/api/app/ContinueReleaseWorkflow'));
            await retainedSave.click();
            await retainedContinued;
            await expect(retainedSave).toBeEnabled({ timeout: 180_000 });
            expect(continueRequests.length).toBeGreaterThan(retainedRequestIndex);
            const retainedRequest = correctionPatchRequest(continueRequests, retainedRequestIndex);
            expect(retainedRequest.intent?.correctionPatch?.expectedRevision).toBe(retainedPriorRevision);
            current = await api('GetReleaseWorkflow', { workflowId: lane.workflowId });
            assertInputBoundary(current, lane.trackerIds);
            expect(correctionRevision(current)).toBe(retainedPriorRevision + 1);
            for (const retained of retainedRefs) expect(explicitRefs(current).has(refKey(retained))).toBe(true);
            fs.writeFileSync(retainedPath, JSON.stringify({ laneId: lane.laneId, refs: retainedRefs, commentary, correctionRevision: correctionRevision(current), effectiveFactsHash: hash(current.release.release) }, null, 2));
          }
        }
      }

      fs.writeFileSync(path.join(runDir, 'snapshots', `${lane.laneId}.private.json`), JSON.stringify(current, null, 2));
      results.push({ caseId: lane.caseId, laneId: lane.laneId, stage: 'input_dom', status: 'pass', reason: 'input_controls_readiness_and_history_visible', evidence: { readinessFields: current.inputReadiness.fields?.length || 0, correctionRows: await rows.count(), sourceLookup: Boolean(sourceInstructions.SourceLookup), trackerSourceIDs: Object.keys(sourceInstructions.TrackerIDs || {}).length } });
    }

    if (phase !== 'input-restart') {
      for (const field of correctionFields) {
        const row = fieldLedger.get(field);
        results.push({ caseId: row.laneId ? lanes.find(lane => lane.laneId === row.laneId)?.caseId || '' : '', laneId: row.laneId || '', stage: `input_field_${field}`, status: row.status, reason: row.reason, evidence: { attempts: row.attempts.length } });
      }
      fs.writeFileSync(path.join(runDir, 'input-field-ledger.private.json'), JSON.stringify([...fieldLedger.values()], null, 2));
    }
    expect(excludedRequests, 'input_browser_crossed_effect_boundary').toBe(0);
    results.push({ caseId: '', laneId: '', stage: 'input_browser_excluded_requests', status: 'pass', reason: 'zero_excluded_browser_requests', evidence: { excludedRequests } });
  } finally {
    fs.writeFileSync(path.join(runDir, `${prefix}-results.json`), JSON.stringify({ requests, results }, null, 2));
    await context.close();
  }
});
