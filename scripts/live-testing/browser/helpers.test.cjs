const { test } = require('node:test');
const assert = require('node:assert/strict');
const { createRequestPacer, isJournaledImageURL, hostedImageDecodeURL, compareMediaOrder, screenshotLifecyclePlan, recaptureScreenshotProbe } = require('./helpers.cjs');

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

test('accept exact journaled HTTPS URLs across providers and reject unjournaled URLs or unsafe schemes', () => {
  const returned = ['https://lostimg.cc/a.png', 'https://i.ptscreens.com/b.png', 'https://images.example.test/c.png'];
  const unsafe = ['http://images.example.test/a.png', 'https://user:password@images.example.test/a.png', 'data:image/png;base64,AA==', 'not a URL'];
  const journaled = new Set([...returned, ...unsafe]);
  for (const value of returned) assert.equal(isJournaledImageURL(value, journaled), true);
  for (const value of [...unsafe, 'https://lostimg.cc/other.png', 'https://i.ptscreens.com/b.png?different=true']) assert.equal(isJournaledImageURL(value, journaled), false);
});

test('decode typed image URLs while retaining exact viewer, provider, and journal association', () => {
  const artifact = { host: 'ptscreens', url: 'https://images.example.test/view/synthetic' };
  const image = 'https://cdn.example.test/synthetic.png';
  const link = { Host: 'ptscreens', WebURL: artifact.url, ImgURL: image };
  const journal = [{ kind: 'uploaded', provider: artifact.host, urls: [image, artifact.url] }];
  assert.equal(hostedImageDecodeURL(artifact, [link], journal), image);
  assert.equal(hostedImageDecodeURL(artifact, [link, link], journal), '');
  assert.equal(hostedImageDecodeURL(artifact, [{ ...link, ImgURL: '', RawURL: image }], journal), image);
  assert.equal(hostedImageDecodeURL(artifact, [{ ...link, Host: 'imgbb' }], journal), '');
  assert.equal(hostedImageDecodeURL(artifact, [{ ...link, WebURL: 'https://images.example.test/view/other' }], journal), '');
  assert.equal(hostedImageDecodeURL(artifact, [link], [{ ...journal[0], provider: 'imgbb' }]), '');
  assert.equal(hostedImageDecodeURL(artifact, [link], [{ ...journal[0], urls: [image] }, { ...journal[0], urls: [artifact.url] }]), '');
  const other = 'https://cdn.example.test/other.png';
  assert.equal(hostedImageDecodeURL(artifact, [link, { ...link, ImgURL: other }], [{ ...journal[0], urls: [...journal[0].urls, other] }]), '');
  const unsafe = 'http://cdn.example.test/synthetic.png';
  assert.equal(hostedImageDecodeURL(artifact, [{ ...link, ImgURL: unsafe }], [{ ...journal[0], urls: [artifact.url, unsafe] }]), '');
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
