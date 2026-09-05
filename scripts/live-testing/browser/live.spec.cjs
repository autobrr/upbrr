const fs = require('node:fs');
const path = require('node:path');
const { execFileSync } = require('node:child_process');
const { randomUUID, createHash } = require('node:crypto');
const { isLostimgImageURL, compareMediaOrder, screenshotLifecyclePlan, recaptureScreenshotProbe } = require('./helpers.cjs');
const { test, expect } = require('../../../webui/node_modules/@playwright/test');

test('owned embedded live runtime, controls, local images, and selection persistence', async ({ browser }) => {
  const runDir = process.env.UPBRR_LIVE_RUN_DIR;
  if (!runDir || !path.isAbsolute(runDir)) throw new Error('live_run_directory_required');
  const privateRoot = path.join(process.env.LOCALAPPDATA, 'upbrr-live-testing', 'runs') + path.sep;
  if (!path.resolve(runDir).toLowerCase().startsWith(privateRoot.toLowerCase())) throw new Error('private_run_directory_required');
  const handoff = JSON.parse(fs.readFileSync(path.join(runDir, 'browser.private.json'), 'utf8'));
  if (handoff.baseURL !== 'http://127.0.0.1:7480' || !Number.isInteger(handoff.process.pid)) throw new Error('live_origin_or_process_invalid');
  if (fs.existsSync(path.join(runDir, 'cleanup-started'))) throw new Error('cleanup_already_started');
  // Check the current OS process/listener as well as the HTTP identity before touching the application.
  const processProbe = `$p=Get-Process -Id ${handoff.process.pid} -ErrorAction Stop; $owners=@(Get-NetTCPConnection -State Listen -LocalPort 7480 | Select-Object -ExpandProperty OwningProcess -Unique); @{path=$p.Path;startTicks=$p.StartTime.ToUniversalTime().Ticks;owners=$owners}|ConvertTo-Json -Compress`;
  const processState = JSON.parse(execFileSync('pwsh', ['-NoProfile', '-EncodedCommand', Buffer.from(processProbe, 'utf16le').toString('base64')], { windowsHide: true, encoding: 'utf8' }));
  expect(processState.path).toBe(handoff.process.path);
  // JSON stores ticks as a Number here; both values were produced from the same exact private record.
  expect(processState.startTicks).toBe(handoff.process.startTicks);
  expect(processState.owners).toEqual([handoff.process.pid]);
  const context = await browser.newContext({ baseURL: handoff.baseURL });
  await context.addCookies(handoff.cookies);
  const page = await context.newPage();
  const authResponse = await context.request.get('/api/auth/status');
  expect(authResponse.ok()).toBe(true);
  const auth = await authResponse.json();
  expect(auth.authenticated).toBe(true);
  let requests = 0;
  const requestReceipt = path.join(runDir, handoff.hostedOnly ? 'browser-hosted-requests.private.json' : handoff.restartOnly ? 'browser-restart-requests.private.json' : 'browser-requests.private.json');
  fs.writeFileSync(requestReceipt, JSON.stringify({ requests }));
  const api = async (method, body = {}) => {
    if (requests >= handoff.remainingRequests) throw new Error('browser_request_budget_exhausted');
    requests++;
    fs.writeFileSync(requestReceipt, JSON.stringify({ requests }));
    const response = await context.request.post(`/api/app/${method}`, { data: body, headers: { Origin: handoff.baseURL, 'X-CSRF-Token': auth.csrfToken } });
    expect(response.ok(), 'live_api_request_failed').toBe(true);
    return response.json();
  };
  const info = await api('GetApplicationInfo');
  expect(info.buildIdentifier).toBe(handoff.buildIdentifier);
  expect(info.testRuntime).toMatchObject({ mode: 'live_test', runId: handoff.runId, trackerSubmissionAllowed: false, clientMutationAllowed: false, imageUploadsRequireJournal: true, imageUploadLimit: handoff.imageUploadLimit });
  await page.goto('/');
  await expect(page.getByText('Live testing active', { exact: true })).toBeVisible();
  await expect(page.getByRole('button', { name: 'Start upload', exact: true })).toHaveCount(0);
  const results = [];
  const sheet = [];
  const imagesDir = path.join(runDir, handoff.restartOnly ? 'browser-images-restart' : 'browser-images');
  fs.mkdirSync(imagesDir, { recursive: true });
  fs.mkdirSync(path.join(runDir, 'browser-artifacts'), { recursive: true });
  let controlsVerified = false;
  let lifecycleAttempted = false;
  try {
    for (const lane of handoff.lanes.filter(lane => lane.workflowId)) {
      let current = await api('GetReleaseWorkflow', { workflowId: lane.workflowId });
      expect(current.workflow.id).toBe(lane.workflowId);
      const local = (current.media?.artifacts || []).filter(artifact => artifact.kind === 'screenshot');
      if (!local.length && !handoff.hostedOnly) continue;
      await page.evaluate(workflowId => sessionStorage.setItem('upbrr.activeReleaseWorkflow', workflowId), lane.workflowId);
      await page.reload();
      await expect(page.getByText('Live testing active', { exact: true })).toBeVisible();
      if (handoff.hostedOnly) {
        const hosted = (current.media?.artifacts || []).filter(artifact => artifact.kind === 'hosted_image' && artifact.url);
        expect(hosted.length, 'journaled_hosted_images_required').toBeGreaterThan(0);
        const originalIDs = hosted.map(artifact => artifact.id).sort();
        for (let view = 0; view < 2; view++) {
          if (view) await page.reload();
          await page.getByRole('button', { name: 'Upload Images', exact: true }).click();
          await expect(page.getByRole('heading', { name: 'Published images', exact: true })).toBeVisible();
          for (const artifact of hosted) {
            expect(isLostimgImageURL(artifact.url), 'unexpected_hosted_image_origin').toBe(true);
            await expect(page.getByRole('link', { name: artifact.url, exact: true })).toHaveAttribute('href', artifact.url);
            const pixels = await page.evaluate(async url => {
              const img = new Image();
              img.src = url;
              await img.decode();
              return { width: img.naturalWidth, height: img.naturalHeight };
            }, artifact.url);
            expect(pixels.width).toBeGreaterThan(0);
            expect(pixels.height).toBeGreaterThan(0);
          }
          current = await api('GetReleaseWorkflow', { workflowId: lane.workflowId });
          expect(current.media.artifacts.filter(artifact => artifact.kind === 'hosted_image' && artifact.url).map(artifact => artifact.id).sort()).toEqual(originalIDs);
        }
        await page.screenshot({ path: path.join(runDir, 'browser-artifacts', `${lane.laneId}-hosted.private.png`), fullPage: true });
        results.push({ caseId: lane.caseId, laneId: lane.laneId, stage: 'hosted_preview', status: 'pass', reason: 'published_links_decode_and_survive_reload', evidence: { hosted: hosted.length } });
        continue;
      }
      const uploadTab = page.getByRole('button', { name: 'Upload', exact: true });
      if (await uploadTab.isEnabled()) {
        await uploadTab.click();
        await expect(page.getByRole('checkbox', { name: 'Skip client injection' })).toBeChecked();
        await expect(page.getByRole('checkbox', { name: 'Skip client injection' })).toBeDisabled();
        await expect(page.getByRole('button', { name: 'Run dry run', exact: true })).toBeVisible();
        await expect(page.getByRole('button', { name: 'Start upload', exact: true })).toBeDisabled();
        controlsVerified = true;
      }
      let decoded = 0;
      const frames = new Set();
      const imageHashes = new Set();
      let duplicateBytes = 0;
      let observedFrameIdentities = 0;
      for (const [index, artifact] of local.entries()) {
        const params = new URLSearchParams({ workflowId: current.workflow.id, mediaId: current.media.id, mediaRevision: String(current.media.revision), artifactId: artifact.id });
        const resource = `/api/app/release-workflow-media?${params}`;
        const response = await context.request.get(resource);
        expect(response.ok(), 'local_image_unavailable').toBe(true);
        const bytes = await response.body();
        expect(bytes.length).toBeGreaterThan(0);
        const sha256 = createHash('sha256').update(bytes).digest('hex');
        if (imageHashes.has(sha256)) duplicateBytes++;
        imageHashes.add(sha256);
        const mime = response.headers()['content-type']?.split(';')[0];
        expect(['image/png', 'image/jpeg', 'image/webp']).toContain(mime);
        const pixels = await page.evaluate(async ({ data, mime }) => {
          const img = new Image();
          img.src = `data:${mime};base64,${data}`;
          await img.decode();
          return { width: img.naturalWidth, height: img.naturalHeight };
        }, { data: bytes.toString('base64'), mime });
        expect(pixels.width).toBeGreaterThan(0);
        expect(pixels.height).toBeGreaterThan(0);
        if (artifact.width) expect(pixels.width).toBe(artifact.width);
        if (artifact.height) expect(pixels.height).toBe(artifact.height);
        const sourceRatio = lane.sourceDisplayAspect;
        if (sourceRatio) expect(Math.abs(pixels.width / pixels.height - sourceRatio) / sourceRatio, 'display_aspect_ratio_changed').toBeLessThan(0.025);
        if (typeof artifact.timestampSeconds === 'number' && Number.isFinite(artifact.timestampSeconds)) {
          const frame = `${artifact.source || ''}|${artifact.timestampSeconds}`;
          expect(frames.has(frame), 'duplicate_source_frame').toBe(false);
          frames.add(frame);
          observedFrameIdentities++;
        }
        const ext = { 'image/png': 'png', 'image/jpeg': 'jpg', 'image/webp': 'webp' }[mime];
        const filename = `${lane.laneId}-${index + 1}.${ext}`;
        fs.writeFileSync(path.join(imagesDir, filename), bytes);
        sheet.push(`<figure><img src="browser-images/${filename}"><figcaption>${lane.caseId} / ${lane.laneId} / ${index + 1} / ${pixels.width}×${pixels.height}</figcaption></figure>`);
        decoded++;
      }
      results.push({ caseId: lane.caseId, laneId: lane.laneId, stage: 'image_decode', status: 'pass', reason: 'pixels_and_dimensions_verified', evidence: { decoded } });
      results.push({ caseId: lane.caseId, laneId: lane.laneId, stage: 'frame_identity', status: observedFrameIdentities === decoded ? 'pass' : 'inconclusive', reason: observedFrameIdentities === decoded ? 'retained_source_timestamps_unique' : 'frame_identity_metadata_incomplete', evidence: { observed: observedFrameIdentities, decoded } });
      results.push({ caseId: lane.caseId, laneId: lane.laneId, stage: 'frame_content', status: duplicateBytes ? 'inconclusive' : 'pass', reason: duplicateBytes ? 'identical_image_bytes_require_visual_review' : 'image_bytes_distinct', evidence: { decoded, duplicateBytes } });
      if (handoff.restartOnly) {
        results.push({ caseId: lane.caseId, laneId: lane.laneId, stage: 'restart_media_decode', status: 'pass', reason: 'retained_images_decode_after_server_restart', evidence: { decoded } });
        continue;
      }
      if (lane.pendingFeedback) {
        results.push({ caseId: lane.caseId, laneId: lane.laneId, stage: 'selection_lifecycle', status: 'needs_input', reason: 'pending_feedback_authority_preserved', evidence: {} });
        continue;
      }
      const mutation = (method, extra) => api(method, { workflowId: current.workflow.id, expectedRevision: current.workflow.revision, media: { id: current.media.id, revision: current.media.revision }, idempotencyKey: randomUUID(), ...extra });
      const originalSelected = local.filter(artifact => artifact.selected).map(artifact => artifact.id);
      const originalOrder = [...local].sort(compareMediaOrder).map(artifact => artifact.id);
      const id = local[0].id;
      // Retained authority is refreshed after every selection mutation.
      current = await mutation('SetReleaseWorkflowMediaSelection', { artifactIds: [id], selected: false });
      expect(current.media.artifacts.find(artifact => artifact.id === id).selected).toBe(false);
      current = await mutation('SetReleaseWorkflowMediaSelection', { artifactIds: [id], selected: originalSelected.includes(id) });
      if (originalOrder.length > 1) {
        current = await mutation('ReorderReleaseWorkflowMedia', { artifactIds: [...originalOrder].reverse() });
        expect(current.media.artifacts.filter(artifact => artifact.kind === 'screenshot').sort(compareMediaOrder).map(artifact => artifact.id)).toEqual([...originalOrder].reverse());
        current = await mutation('ReorderReleaseWorkflowMedia', { artifactIds: originalOrder });
      }
      await page.reload();
      current = await api('GetReleaseWorkflow', { workflowId: lane.workflowId });
      expect(current.media.artifacts.filter(artifact => artifact.kind === 'screenshot' && artifact.selected).map(artifact => artifact.id).sort()).toEqual([...originalSelected].sort());
      expect(current.media.artifacts.filter(artifact => artifact.kind === 'screenshot').sort(compareMediaOrder).map(artifact => artifact.id)).toEqual(originalOrder);
      fs.writeFileSync(path.join(runDir, 'snapshots', `${lane.laneId}.private.json`), JSON.stringify(current, null, 2));
      results.push({ caseId: lane.caseId, laneId: lane.laneId, stage: 'selection_lifecycle', status: 'pass', reason: 'select_deselect_reorder_reload_restored', evidence: { artifacts: originalOrder.length } });
      if (!lifecycleAttempted) {
        const plan = screenshotLifecyclePlan(current.media.artifacts, await api('GetReleaseWorkflowMediaPlan', { workflowId: lane.workflowId }));
        if (!plan) {
          results.push({ caseId: lane.caseId, laneId: lane.laneId, stage: 'screenshot_delete_recapture', status: 'inconclusive', reason: 'source_frame_plan_unavailable' });
        } else {
          lifecycleAttempted = true;
          const active = value => ['queued', 'running', 'pending'].includes(value.operation?.status);
          const pending = value => [...(value.workflow.requiredActions || []), ...(value.continuation?.requiredActions || []), ...(value.media?.requiredActions || [])].some(action => !action.status || action.status === 'pending');
          const deadline = Date.now() + 180000;
          const wait = async () => {
            while (active(current)) {
              if (Date.now() >= deadline) throw new Error('browser_capture_deadline');
              await new Promise(resolve => setTimeout(resolve, 500));
              current = await api('GetReleaseWorkflow', { workflowId: lane.workflowId });
            }
          };
          const capture = async (selections, cancel) => {
            if (pending(current)) return 'needs_input';
            const idempotencyKey = randomUUID();
            const previousOperation = current.operation?.id;
            for (let transition = 0; transition < 12; transition++) {
              if (Date.now() >= deadline) throw new Error('browser_capture_deadline');
              const revision = current.workflow.revision;
              current = await api('ContinueReleaseWorkflow', { idempotencyKey, goal: 'media_ready', authority: { workflowId: current.workflow.id, expectedRevision: revision }, intent: {
                executionMode: handoff.executionMode, interaction: 'unattended', trackerIds: lane.trackerIds, noSeed: true, skipRemoteDuplicates: false,
                media: { screenshotCount: selections.length, purpose: 'final', selections, captureDvdMenus: false },
              } });
              if (cancel && current.operation?.command === 'capture_media' && current.operation.id !== previousOperation) {
                if (active(current)) await api('CancelReleaseWorkflowOperation', { workflowId: lane.workflowId, operationId: current.operation.id });
                await wait();
                return pending(current) ? 'needs_input' : current.operation?.status === 'canceled' ? 'canceled' : 'completed_before_cancel';
              }
              await wait();
              if (pending(current)) return 'needs_input';
              if (current.workflow.revision === revision) return cancel ? 'completed_before_cancel' : 'completed';
            }
            throw new Error('browser_capture_transition_limit');
          };
          try {
            const previousFingerprint = current.media.captureFingerprint;
            const captured = await recaptureScreenshotProbe(plan, capture, () => current.media.artifacts, async id => {
              current = await mutation('DeleteReleaseWorkflowMedia', { artifactIds: [id] });
            });
            for (const original of plan.local) {
              const retained = current.media.artifacts.find(artifact => artifact.id === original.id);
              expect(retained, 'original_frame_lost_during_probe').toBeTruthy();
              expect([retained.selected, retained.order ?? 0, retained.timestampSeconds]).toEqual([original.selected, original.order ?? 0, original.timestampSeconds]);
            }
            if (captured.status === 'needs_input') {
              results.push({ caseId: lane.caseId, laneId: lane.laneId, stage: 'screenshot_delete_recapture', status: 'needs_input', reason: 'recapture_requires_typed_action' });
              results.push({ caseId: lane.caseId, laneId: lane.laneId, stage: 'screenshot_cancellation', status: 'needs_input', reason: 'recapture_requires_typed_action' });
              results.push({ caseId: lane.caseId, laneId: lane.laneId, stage: 'screenshot_lifecycle_restore', status: 'needs_input', reason: 'pending_feedback_authority_preserved' });
            } else {
              const replacement = current.media.artifacts.find(artifact => artifact.kind === 'screenshot' && artifact.index === plan.probeIndex);
              expect(replacement, 'replacement_frame_missing').toBeTruthy();
              expect(replacement.id).not.toBe(captured.deletedID);
              expect(replacement.timestampSeconds).toBeCloseTo(plan.timestamp, 3);
              expect(current.media.captureFingerprint).not.toBe(previousFingerprint);
              const params = new URLSearchParams({ workflowId: current.workflow.id, mediaId: current.media.id, mediaRevision: String(current.media.revision), artifactId: replacement.id });
              const pixels = await page.evaluate(async resource => {
                const image = new Image(); image.src = resource; await image.decode();
                return { width: image.naturalWidth, height: image.naturalHeight };
              }, `/api/app/release-workflow-media?${params}`);
              expect(pixels.width).toBeGreaterThan(0); expect(pixels.height).toBeGreaterThan(0);
              results.push({ caseId: lane.caseId, laneId: lane.laneId, stage: 'screenshot_delete_recapture', status: 'pass', reason: 'deleted_slot_recaptured_at_new_timestamp_and_decoded' });
              const cancellation = await capture([...plan.selections, plan.cancelSelection], true);
              results.push({ caseId: lane.caseId, laneId: lane.laneId, stage: 'screenshot_cancellation', status: cancellation === 'canceled' ? 'pass' : cancellation === 'needs_input' ? 'needs_input' : 'inconclusive', reason: cancellation === 'canceled' ? 'single_capture_canceled' : cancellation === 'needs_input' ? 'capture_requires_typed_action' : 'capture_completed_before_cancel' });
              if (cancellation === 'needs_input') {
                results.push({ caseId: lane.caseId, laneId: lane.laneId, stage: 'screenshot_lifecycle_restore', status: 'needs_input', reason: 'pending_feedback_authority_preserved' });
                continue;
              }
              // Remove probe frames and preserve the original artifact identities.
              const intended = plan.local.map(old => current.media.artifacts.find(artifact => artifact.id === old.id));
              expect(intended.every(Boolean), 'retained_slot_missing_after_cancel').toBe(true);
              const intendedIDs = intended.map(artifact => artifact.id);
              const extra = current.media.artifacts.filter(artifact => artifact.kind === 'screenshot' && !intendedIDs.includes(artifact.id)).map(artifact => artifact.id);
              if (extra.length) current = await mutation('DeleteReleaseWorkflowMedia', { artifactIds: extra });
              for (const selected of [false, true]) {
                const artifactIds = intended.filter((artifact, index) => Boolean(plan.local[index].selected) === selected).map(artifact => artifact.id);
                if (artifactIds.length) current = await mutation('SetReleaseWorkflowMediaSelection', { artifactIds, selected });
              }
              current = await mutation('ReorderReleaseWorkflowMedia', { artifactIds: intendedIDs });
              await page.reload();
              current = await api('GetReleaseWorkflow', { workflowId: lane.workflowId });
              expect(current.media.artifacts.filter(artifact => artifact.kind === 'screenshot').sort(compareMediaOrder).map(artifact => artifact.id)).toEqual(intendedIDs);
              expect(current.media.artifacts.filter(artifact => artifact.kind === 'screenshot' && artifact.selected).map(artifact => artifact.id).sort()).toEqual(intended.filter((artifact, index) => plan.local[index].selected).map(artifact => artifact.id).sort());
              results.push({ caseId: lane.caseId, laneId: lane.laneId, stage: 'screenshot_lifecycle_restore', status: 'pass', reason: 'intended_slots_selection_and_order_retained' });
            }
          } finally {
            fs.writeFileSync(path.join(runDir, 'snapshots', `${lane.laneId}.private.json`), JSON.stringify(current, null, 2));
          }
        }
      } else results.push({ caseId: lane.caseId, laneId: lane.laneId, stage: 'screenshot_delete_recapture', status: 'not_applicable', reason: 'single_lifecycle_probe_already_attempted' });
      const screenshotTab = page.getByRole('button', { name: 'Screenshots', exact: true });
      if (await screenshotTab.isEnabled()) {
        await screenshotTab.click();
        await page.screenshot({ path: path.join(runDir, 'browser-artifacts', `${lane.laneId}.private.png`), fullPage: true });
      }
    }
    if (!handoff.hostedOnly && !handoff.restartOnly) {
      results.push({ caseId: '', laneId: '', stage: 'upload_controls', status: controlsVerified ? 'pass' : handoff.requireUploadControls ? 'inconclusive' : 'not_applicable', reason: controlsVerified ? 'dry_run_and_locked_no_seed_verified' : handoff.requireUploadControls ? 'no_eligible_upload_page' : 'outside_selected_suite' });
      if (!lifecycleAttempted) results.push({ caseId: '', laneId: '', stage: 'screenshot_cancellation', status: 'inconclusive', reason: 'no_eligible_lifecycle_workflow' });
    }
  } finally {
    fs.writeFileSync(path.join(runDir, handoff.hostedOnly ? 'browser-hosted-results.json' : handoff.restartOnly ? 'browser-restart-results.json' : 'browser-results.json'), JSON.stringify({ requests, results }, null, 2));
    if (!handoff.hostedOnly && !handoff.restartOnly) fs.writeFileSync(path.join(runDir, 'contact-sheet.private.html'), `<!doctype html><meta charset="utf-8"><title>Private live capture review</title><style>body{background:#111;color:#eee;font:16px sans-serif}main{display:grid;grid-template-columns:repeat(2,minmax(0,1fr))}figure{margin:12px}img{width:100%;height:auto}figcaption{padding:8px}</style><h1>Private capture review</h1><p>Review tone mapping, deinterlacing, crop, source scene, and frame choice. Automated decoding does not establish visual quality.</p><main>${sheet.join('')}</main>`);
    await context.close();
  }
});
