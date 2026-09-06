// Share one queue between page traffic and direct API requests (server limit: 300/minute).
function createRequestPacer() {
  let turn = Promise.resolve();
  return () => {
    turn = turn.then(() => new Promise(resolve => setTimeout(resolve, 250)));
    return turn;
  };
}

function isJournaledImageURL(value, journaledURLs) {
  try {
    const url = new URL(value);
    return url.protocol === 'https:' && !url.username && !url.password && journaledURLs.has(value);
  } catch { return false; }
}

// Public artifacts prefer viewer links; typed history retains the image URL.
function hostedImageDecodeURL(artifact, links, journal) {
  const matches = links.filter(link => {
    const published = [link.WebURL, link.ImgURL, link.RawURL].map(value => (value || '').trim()).find(Boolean);
    return (link.Host || '').trim().toLowerCase() === artifact.host && published === artifact.url;
  });
  if (matches.length !== 1) return '';
  const image = (matches[0].ImgURL || matches[0].RawURL || '').trim();
  return journal.some(record => {
    if (record.kind !== 'uploaded' || record.provider !== artifact.host) return false;
    const urls = new Set(record.urls || []);
    return isJournaledImageURL(artifact.url, urls) && isJournaledImageURL(image, urls);
  }) ? image : '';
}

function compareMediaOrder(left, right) {
  return (left.order ?? 0) - (right.order ?? 0);
}

function screenshotLifecyclePlan(artifacts, plan) {
  const local = artifacts.filter(artifact => artifact.kind === 'screenshot').sort(compareMediaOrder);
  if (!local.length || plan.discType || !Number.isFinite(plan.durationSeconds) || plan.durationSeconds <= 4) return null;
  const indices = local.map(artifact => artifact.index || 0);
  if (new Set(indices).size !== indices.length || indices.some(index => !Number.isInteger(index) || index < 0) ||
      local.some(artifact => !Number.isFinite(artifact.timestampSeconds) || artifact.timestampSeconds <= 0)) return null;
  const target = local[0];
  const timestamps = [target.timestampSeconds + 1, target.timestampSeconds - 1, target.timestampSeconds + 2, target.timestampSeconds - 2].filter(value =>
    value > 0 && value < plan.durationSeconds - 1 && !local.some(artifact => artifact.timestampSeconds === value));
  if (timestamps.length < 2) return null;
  const [probeTimestamp, timestamp] = timestamps;
  const probeIndex = Math.max(...indices) + 1;
  const selections = local.map(artifact => ({ Index: artifact.index || 0, TimestampSeconds: artifact.timestampSeconds, Frame: 0, Source: artifact.source || '' }));
  selections.push({ Index: probeIndex, TimestampSeconds: timestamp, Frame: 0, Source: target.source || '' });
  const initialSelections = selections.map(selection => selection.Index === probeIndex ? { ...selection, TimestampSeconds: probeTimestamp } : selection);
  return { local, probeIndex, timestamp, initialSelections, selections, cancelSelection: { Index: probeIndex + 1, TimestampSeconds: timestamp, Frame: 0, Source: target.source || '' } };
}

// Delete only a newly captured probe; required actions leave every original slot intact.
async function recaptureScreenshotProbe(plan, capture, artifacts, remove) {
  if (await capture(plan.initialSelections, false) === 'needs_input') return { status: 'needs_input' };
  const probe = artifacts().find(artifact => artifact.kind === 'screenshot' && artifact.index === plan.probeIndex);
  if (!probe || plan.local.some(artifact => artifact.id === probe.id)) throw new Error('new_probe_frame_missing');
  await remove(probe.id);
  if (artifacts().some(artifact => artifact.id === probe.id)) throw new Error('probe_frame_not_deleted');
  return { status: await capture(plan.selections, false), deletedID: probe.id };
}

module.exports = { createRequestPacer, isJournaledImageURL, hostedImageDecodeURL, compareMediaOrder, screenshotLifecyclePlan, recaptureScreenshotProbe };
