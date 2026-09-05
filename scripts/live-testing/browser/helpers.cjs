function isLostimgImageURL(value) {
  try {
    const url = new URL(value);
    return url.protocol === 'https:' && !url.username && !url.password &&
      (url.hostname === 'lostimg.cc' || url.hostname.endsWith('.lostimg.cc'));
  } catch { return false; }
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
  const timestamp = [target.timestampSeconds + 1, target.timestampSeconds - 1].find(value =>
    value > 0 && value < plan.durationSeconds - 1 && !local.some(artifact => artifact.timestampSeconds === value));
  if (timestamp === undefined) return null;
  const selections = local.map(artifact => ({ Index: artifact.index || 0, TimestampSeconds: artifact.id === target.id ? timestamp : artifact.timestampSeconds, Frame: 0, Source: artifact.source || '' }));
  return { local, target, timestamp, selections, cancelSelection: { Index: Math.max(...indices) + 1, TimestampSeconds: timestamp, Frame: 0, Source: target.source || '' } };
}

module.exports = { isLostimgImageURL, compareMediaOrder, screenshotLifecyclePlan };
