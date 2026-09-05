const { test } = require('node:test');
const assert = require('node:assert/strict');
const { createRequestPacer, isLostimgImageURL, compareMediaOrder, screenshotLifecyclePlan, recaptureScreenshotProbe } = require('./helpers.cjs');

test('concurrent page and direct API requests share serial pacing instead of bursting', async t => {
  const timers = [];
  t.mock.method(global, 'setTimeout', (callback, delay) => { timers.push({ callback, delay }); });
  const pace = createRequestPacer();
  const dispatched = [];
  const requests = ['page', 'api', 'page'].map(source => pace().then(() => dispatched.push(source)));
  await Promise.resolve();
  for (let index = 0; index < 3; index++) {
    assert.equal(timers.length, index + 1);
    assert.equal(timers[index].delay, 250);
    assert.equal(dispatched.length, index);
    timers[index].callback();
    await new Promise(resolve => setImmediate(resolve));
  }
  await Promise.all(requests);
  assert.deepEqual(dispatched, ['page', 'api', 'page']);
});

test('accept production Lostimg image URLs and reject unrelated origins and schemes', () => {
  for (const value of ['https://lostimg.cc/a.png', 'https://i.lostimg.cc/b.png']) assert.equal(isLostimgImageURL(value), true);
  for (const value of ['http://lostimg.cc/a.png', 'https://lostimg.com/a.png', 'https://lostimg.cc.example.com/a.png', 'https://example.com/lostimg.cc', 'https://user:password@lostimg.cc/a.png', 'data:image/png;base64,AA==']) assert.equal(isLostimgImageURL(value), false);
});

test('recapture and cancellation use spare slots without changing original selections', () => {
  const images = [{ id: 'a', kind: 'screenshot', timestampSeconds: 10, selected: true }, { id: 'b', kind: 'screenshot', index: 1, timestampSeconds: 20, order: 1 }];
  const plan = screenshotLifecyclePlan(images, { durationSeconds: 30 });
  assert.equal(plan.probeIndex, 2);
  assert.deepEqual(plan.initialSelections.map(item => [item.Index, item.TimestampSeconds]), [[0, 10], [1, 20], [2, 11]]);
  assert.deepEqual(plan.selections.map(item => [item.Index, item.TimestampSeconds]), [[0, 10], [1, 20], [2, 9]]);
  assert.equal(plan.cancelSelection.Index, 3);
  assert.equal(images[0].timestampSeconds, 10);
  assert.equal(screenshotLifecyclePlan(images, { durationSeconds: 30, discType: 'BDMV' }), null);
  assert.equal(screenshotLifecyclePlan([{ ...images[0], timestampSeconds: undefined }], { durationSeconds: 30 }), null);
  assert.equal(screenshotLifecyclePlan([images[0], { ...images[1], index: 0 }], { durationSeconds: 30 }), null);
});

test('typed actions before and after probe deletion preserve original frames and stop further mutations', async () => {
  for (const blockedCapture of [1, 2, 0]) {
    const original = [{ id: 'original-a', kind: 'screenshot', timestampSeconds: 10, selected: true }, { id: 'original-b', kind: 'screenshot', index: 1, timestampSeconds: 20, selected: false, order: 1 }];
    const plan = screenshotLifecyclePlan(original, { durationSeconds: 30 });
    let retained = structuredClone(original);
    let captures = 0;
    const deleted = [];
    const result = await recaptureScreenshotProbe(plan, async selections => {
      captures++;
      assert.deepEqual(selections.slice(0, 2).map(item => [item.Index, item.TimestampSeconds]), [[0, 10], [1, 20]]);
      if (captures === blockedCapture) return 'needs_input';
      const selection = selections.find(item => item.Index === plan.probeIndex);
      // Production IDs are deterministic for the same capture inputs.
      retained.push({ id: `probe-${selection.Index}@${selection.TimestampSeconds}`, kind: 'screenshot', index: selection.Index, timestampSeconds: selection.TimestampSeconds });
      return 'completed';
    }, () => retained, async id => {
      deleted.push(id);
      retained = retained.filter(artifact => artifact.id !== id);
    });
    assert.deepEqual(retained.slice(0, 2), original);
    assert.deepEqual(deleted, blockedCapture === 1 ? [] : ['probe-2@11']);
    assert.equal(captures, blockedCapture === 1 ? 1 : 2);
    assert.equal(result.status, blockedCapture ? 'needs_input' : 'completed');
    assert.equal(retained.length, blockedCapture ? 2 : 3);
    if (!blockedCapture) {
      assert.notEqual(retained[2].id, result.deletedID);
      assert.equal(retained[2].timestampSeconds, plan.timestamp);
    }
  }
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
