const { test } = require('node:test');
const assert = require('node:assert/strict');
const { isLostimgImageURL, compareMediaOrder, screenshotLifecyclePlan } = require('./helpers.cjs');

test('accept production Lostimg image URLs and reject unrelated origins and schemes', () => {
  for (const value of ['https://lostimg.cc/a.png', 'https://i.lostimg.cc/b.png']) assert.equal(isLostimgImageURL(value), true);
  for (const value of ['http://lostimg.cc/a.png', 'https://lostimg.com/a.png', 'https://lostimg.cc.example.com/a.png', 'https://example.com/lostimg.cc', 'https://user:password@lostimg.cc/a.png', 'data:image/png;base64,AA==']) assert.equal(isLostimgImageURL(value), false);
});

test('recapture uses a deleted logical slot and cancellation uses a new slot', () => {
  const images = [{ id: 'a', kind: 'screenshot', timestampSeconds: 10, selected: true }, { id: 'b', kind: 'screenshot', index: 1, timestampSeconds: 20, order: 1 }];
  const plan = screenshotLifecyclePlan(images, { durationSeconds: 30 });
  assert.equal(plan.target.id, 'a');
  assert.deepEqual(plan.selections.map(item => [item.Index, item.TimestampSeconds]), [[0, 11], [1, 20]]);
  assert.equal(plan.cancelSelection.Index, 2);
  assert.equal(images[0].timestampSeconds, 10);
  assert.equal(screenshotLifecyclePlan(images, { durationSeconds: 30, discType: 'BDMV' }), null);
  assert.equal(screenshotLifecyclePlan([{ ...images[0], timestampSeconds: undefined }], { durationSeconds: 30 }), null);
  assert.equal(screenshotLifecyclePlan([images[0], { ...images[1], index: 0 }], { durationSeconds: 30 }), null);
});

test('Go omitempty order zero sorts first even when capture slots remain in their original array positions', () => {
  const wireArtifacts = JSON.parse('[{"id":"screen-a","index":0,"order":3},{"id":"screen-b","index":1,"order":2},{"id":"screen-c","index":2,"order":1},{"id":"screen-d","index":3}]');
  const ordered = [...wireArtifacts].sort(compareMediaOrder);
  assert.deepEqual(ordered.map(artifact => artifact.id), ['screen-d', 'screen-c', 'screen-b', 'screen-a']);
  assert.deepEqual(ordered.map(artifact => artifact.index), [3, 2, 1, 0]);
  assert.deepEqual(wireArtifacts.map(artifact => artifact.id), ['screen-a', 'screen-b', 'screen-c', 'screen-d']);
  assert.equal(compareMediaOrder({ order: 0 }, {}), 0);
  const restored = JSON.parse('[{"id":"screen-c","order":2},{"id":"screen-a"},{"id":"screen-d","order":3},{"id":"screen-b","order":1}]');
  assert.deepEqual(restored.sort(compareMediaOrder).map(artifact => artifact.id), ['screen-a', 'screen-b', 'screen-c', 'screen-d']);
});
