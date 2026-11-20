// What the pre-send tracking check (LST-44) says for each kind of report.
import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import {
  codeParts, trackingConfirmLines, trackingRows, trackingTag, trackingToastParts, warningsCovered,
} from '../src/tracking-check.js';

const report = (over = {}) => ({
  state: 'ok',
  disabled: false,
  individual: true,
  applicable: true,
  auto: false,
  contentType: 'richtext',
  messenger: 'email',
  view: 'manual',
  ...over,
  links: {
    total: 0, manual: 0, auto: 0, untracked: 0, untrackedUrls: [], ...(over.links || {}),
  },
});
const brief = (rows) => rows.map((r) => `${r.name}:${r.level}:${r.state.split('.').pop()}`);
const keys = (items) => items.map((i) => i.key.replace('campaigns.tracking.', ''));

test('a tracked HTML mail is ok and adds nothing to the confirmation or the notice', () => {
  const r = report({ links: { total: 3, manual: 3 } });
  assert.deepEqual(brief(trackingRows(r)), ['view:ok:manual', 'click:ok:all']);
  assert.equal(trackingTag(r).key, 'campaigns.tracking.ok');
  assert.deepEqual(trackingConfirmLines(r), []);
  assert.deepEqual(trackingToastParts(r), []);
});

test('no pixel and plain links warn in the box, the confirmation and the notice', () => {
  const r = report({ state: 'warn', view: 'missing', links: { total: 3, manual: 1, untracked: 2 } });
  const rows = trackingRows(r);
  assert.deepEqual(brief(rows), ['view:warn:missing', 'click:warn:someUntracked']);
  assert.equal(rows[0].help, 'campaigns.tracking.view.missingHelp');
  assert.equal(rows[1].help, 'campaigns.tracking.click.untrackedHelp');
  assert.deepEqual(rows[1].stateParams, { total: 3, auto: 0, untracked: 2 });
  assert.equal(trackingTag(r).type, 'is-warning');
  assert.deepEqual(keys(trackingConfirmLines(r)), ['confirm.viewMissing', 'confirm.untracked']);
  assert.deepEqual(trackingConfirmLines(r)[1].params, { n: 2 });
  assert.deepEqual(keys(trackingToastParts(r)), ['toast.viewMissing', 'toast.untracked']);
});

test('a visual e-mail is automatic', () => {
  const r = report({
    auto: true, contentType: 'visual', view: 'auto', links: { total: 4, auto: 4 },
  });
  const rows = trackingRows(r);
  assert.deepEqual(brief(rows), ['view:ok:auto', 'click:ok:allAuto']);
  assert.equal(rows[0].help, 'campaigns.tracking.view.autoHelp');
});

test('plain text: views are a grey note, only untracked links warn', () => {
  const tracked = report({ contentType: 'plain', view: 'unsupported', links: { total: 1, manual: 1 } });
  assert.deepEqual(brief(trackingRows(tracked)), ['view:na:unsupported', 'click:ok:all']);
  assert.deepEqual(trackingConfirmLines(tracked), []);

  const r = report({
    state: 'warn', contentType: 'plain', view: 'unsupported', links: { total: 2, manual: 1, untracked: 1 },
  });
  assert.deepEqual(brief(trackingRows(r)), ['view:na:unsupported', 'click:warn:someUntracked']);
  assert.deepEqual(keys(trackingConfirmLines(r)), ['confirm.untracked']);
  assert.deepEqual(keys(trackingToastParts(r)), ['toast.untracked']);
});

test('a messenger that is not e-mail', () => {
  const r = report({
    state: 'warn', applicable: false, contentType: 'visual', messenger: 'webhook', view: 'n/a', links: { total: 4, untracked: 4 },
  });
  const rows = trackingRows(r);
  assert.deepEqual(brief(rows), ['view:na:na', 'click:warn:noneTracked']);
  assert.deepEqual(rows[0].helpParams, { messenger: 'webhook' });
  assert.equal(rows[1].help, 'campaigns.tracking.click.notEmailHelp');
});

test('no links is not a warning', () => {
  assert.deepEqual(brief(trackingRows(report())), ['view:ok:manual', 'click:na:none']);
});

test('tracking off for the instance', () => {
  const r = report({
    state: 'off', disabled: true, individual: false, view: 'off',
  });
  assert.deepEqual(brief(trackingRows(r)), ['view:off:off', 'click:off:off']);
  assert.equal(trackingTag(r).type, 'is-danger is-light');
  assert.deepEqual(keys(trackingConfirmLines(r)), ['confirm.off']);
  assert.deepEqual(keys(trackingToastParts(r)), ['toast.off']);
});

test('individual tracking off adds a note and no warning', () => {
  const r = report({ individual: false, links: { total: 3, manual: 3 } });
  assert.deepEqual(brief(trackingRows(r)), ['view:ok:manual', 'click:ok:all', 'individual:na:off']);
  assert.equal(trackingTag(r).key, 'campaigns.tracking.ok');
  assert.deepEqual(trackingConfirmLines(r), []);
});

test('a check that could not be made is said in the confirmation, not in the test notice', () => {
  assert.deepEqual(keys(trackingConfirmLines(null)), ['confirm.unknown']);
  assert.deepEqual(trackingToastParts(null), []);
  assert.deepEqual(trackingRows(null), []);
  assert.equal(trackingTag(null).key, 'campaigns.tracking.failed');
});

test('code marks split a help text', () => {
  assert.deepEqual(codeParts('Add `{{ TrackView }}` to it.'), ['Add ', '{{ TrackView }}', ' to it.']);
  assert.deepEqual(codeParts('No code.'), ['No code.']);
});

// Every key this module can return exists in both languages, with the params it passes.
test('every key exists in ko and en', () => {
  const cases = [
    report({ links: { total: 3, manual: 3 } }),
    report({ state: 'warn', view: 'missing', links: { total: 3, manual: 1, untracked: 2 } }),
    report({ auto: true, contentType: 'visual', view: 'auto', links: { total: 4, auto: 4 } }),
    report({ state: 'warn', contentType: 'plain', view: 'unsupported', links: { total: 2, untracked: 2 } }),
    report({ state: 'warn', applicable: false, contentType: 'visual', messenger: 'webhook', view: 'n/a', links: { total: 4, untracked: 4 } }),
    report({ state: 'off', disabled: true, view: 'off' }),
    report({ individual: false }),
    null,
  ];
  const used = new Set();
  cases.forEach((r) => {
    used.add(trackingTag(r).key);
    trackingRows(r).forEach((row) => [row.label, row.state, row.help].filter(Boolean).forEach((k) => used.add(k)));
    trackingConfirmLines(r).forEach((l) => used.add(l.key));
    trackingToastParts(r).forEach((p) => used.add(p.key));
  });
  ['ko', 'en'].forEach((lang) => {
    const dict = JSON.parse(fs.readFileSync(new URL(`../../i18n/${lang}.json`, import.meta.url)));
    used.forEach((k) => assert.ok(dict[k], `${lang}: ${k}`));
  });
});

test('a later check needs no new confirmation only when it warns about nothing new', () => {
  const ok = trackingConfirmLines(report());
  const missing = trackingConfirmLines(report({ state: 'warn', view: 'missing' }));
  const two = trackingConfirmLines(report({ state: 'warn', links: { total: 2, untracked: 2 } }));
  const three = trackingConfirmLines(report({ state: 'warn', links: { total: 3, untracked: 3 } }));
  const unknown = trackingConfirmLines(null);
  assert.equal(warningsCovered(ok, ok), true);
  assert.equal(warningsCovered(missing, ok), true);
  assert.equal(warningsCovered(ok, missing), false);
  assert.equal(warningsCovered(two, two), true);
  assert.equal(warningsCovered(two, three), false);
  assert.equal(warningsCovered(unknown, missing), false);
  assert.equal(warningsCovered(missing, unknown), false);
});
