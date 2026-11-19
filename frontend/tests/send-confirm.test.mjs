// What the pre-send confirmation (LST-48) says for an answer of the preflight API.
import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import {
  checkLines, offsetLabel, richParts, scheduleDisplay, sendConfirmModel, timeUntil, untilKey,
} from '../src/send-confirm.js';

const NOW = new Date('2026-11-19T12:00:00Z');
const trackingOk = {
  state: 'ok', view: 'manual', links: { total: 2, manual: 2, auto: 0, untracked: 0, untrackedUrls: [] },
};
const pf = (over = {}) => ({
  mode: 'start',
  name: 'C',
  recipients: 300,
  remaining: 300,
  sent: 0,
  toSend: 0,
  sendAt: null,
  asOf: '2026-11-19T05:37:00Z',
  lists: [{ id: 1, name: 'A', count: 280 }, { id: 2, name: 'B', count: 40 }],
  checks: [
    { key: 'from.placeholder', level: 'ok' },
    { key: 'unsubscribe.missing', level: 'ok' },
  ],
  tracking: trackingOk,
  ...over,
});
const model = (over, opts) => sendConfirmModel(pf(over), { now: NOW, tz: 'Asia/Seoul', ...opts });

test('start: title and button carry the number, nothing is disabled', () => {
  const m = model();
  assert.equal(m.title.key, 'campaigns.sendConfirm.title.start');
  assert.deepEqual(m.title.params, { n: 300 });
  assert.equal(m.sub.key, 'campaigns.sendConfirm.sub.start');
  assert.equal(m.confirm.key, 'campaigns.sendConfirm.confirm.start');
  assert.equal(m.disabled, false);
  assert.equal(m.warnings, 0);
  assert.equal(m.when, null);
});

test('warnings rename the button and count in the subtitle, but do not disable it', () => {
  const m = model({
    checks: [{ key: 'from.placeholder', level: 'warn', params: { from: 'noreply@x' } }, { key: 'recent.notApplied', level: 'warn', params: { days: 14, excluded: 42 } }],
    tracking: { ...trackingOk, view: 'missing', links: { ...trackingOk.links, untracked: 3 } },
  });
  assert.equal(m.warnings, 4);
  assert.deepEqual(m.sub, { key: 'campaigns.sendConfirm.sub.warn', params: { n: 4 } });
  assert.equal(m.confirm.key, 'campaigns.sendConfirm.confirmAnyway.start');
  assert.equal(m.disabled, false);
});

test('schedule: the time with its zone, the time left, and the title in the zone', () => {
  const m = model({ mode: 'schedule', sendAt: '2026-11-20T00:00:00Z' });
  assert.equal(m.title.key, 'campaigns.sendConfirm.title.schedule');
  assert.deepEqual(m.title.params, { n: 300, month: '11', day: '20', time: '09:00' });
  assert.equal(m.when.text, '2026-11-20 (금) 09:00');
  assert.equal(m.when.zone, 'Asia/Seoul');
  assert.equal(m.when.offset, 'UTC+9');
  assert.equal(m.confirm.key, 'campaigns.sendConfirm.confirm.schedule');
  assert.deepEqual(m.until, { days: 0, hours: 12, minutes: 0 });
  assert.equal(m.other, null, 'Korean time needs no second line');
});

test('schedule in another zone adds the Korean time of the same moment', () => {
  const m = model({ mode: 'schedule', sendAt: '2026-11-20T14:00:00Z' }, { tz: 'America/New_York' });
  assert.equal(m.when.text, '2026-11-20 (금) 09:00');
  assert.equal(m.when.offset, 'UTC-5');
  assert.equal(m.other.text, '2026-11-20 (금) 23:00');
  assert.equal(m.other.offset, 'UTC+9');
});

test('time zones: Asia/Seoul, UTC, America/Los_Angeles (with and without summer time), half hours', () => {
  const at = (iso, tz) => { const d = scheduleDisplay(iso, { tz }); return `${d.text} ${d.zone} ${d.offset}`; };
  assert.equal(at('2026-11-20T00:00:00Z', 'Asia/Seoul'), '2026-11-20 (금) 09:00 Asia/Seoul UTC+9');
  assert.equal(at('2026-11-20T00:00:00Z', 'UTC'), '2026-11-20 (금) 00:00 UTC UTC');
  assert.equal(at('2026-11-20T00:00:00Z', 'America/Los_Angeles'), '2026-11-19 (목) 16:00 America/Los_Angeles UTC-8');
  assert.equal(at('2026-07-01T00:00:00Z', 'America/Los_Angeles'), '2026-06-30 (화) 17:00 America/Los_Angeles UTC-7');
  assert.equal(at('2026-11-20T00:00:00Z', 'Asia/Kolkata'), '2026-11-20 (금) 05:30 Asia/Kolkata UTC+5:30');
  assert.equal(offsetLabel(0), 'UTC');
  assert.equal(offsetLabel(-210), 'UTC-3:30');
  // Midnight does not become 24:00.
  assert.equal(scheduleDisplay('2026-11-19T15:00:00Z', { tz: 'Asia/Seoul' }).time, '00:00');
});

test('time left: largest unit, nothing for a moment that has passed', () => {
  assert.equal(timeUntil('2026-11-19T11:59:00Z', NOW), null);
  assert.deepEqual(timeUntil('2026-11-20T13:30:00Z', NOW), { days: 1, hours: 1, minutes: 30 });
  assert.equal(untilKey({ days: 1, hours: 1, minutes: 30 }).key, 'campaigns.sendConfirm.until.days');
  assert.equal(untilKey({ days: 0, hours: 2, minutes: 5 }).key, 'campaigns.sendConfirm.until.hours');
  assert.equal(untilKey({ days: 0, hours: 0, minutes: 5 }).key, 'campaigns.sendConfirm.until.minutes');
});

test('resume: the number is who is left, not the whole', () => {
  const m = model({ mode: 'resume', remaining: 120, recipients: 300, sent: 180, toSend: 300 });
  assert.equal(m.count, 120);
  assert.deepEqual(m.title, { key: 'campaigns.sendConfirm.title.resume', params: { n: 120 } });
  assert.deepEqual(m.confirm.params, { n: 120 });
  assert.equal(m.confirm.key, 'campaigns.sendConfirm.confirm.resume');
});

test('resume with a send time: the time is shown, the title says when, a passed time does not', () => {
  const ahead = model({ mode: 'resume', remaining: 12, sendAt: '2026-11-20T00:00:00Z' });
  assert.equal(ahead.title.key, 'campaigns.sendConfirm.title.resumeAt');
  assert.deepEqual(ahead.title.params, { n: 12, month: '11', day: '20', time: '09:00' });
  assert.equal(ahead.sub.key, 'campaigns.sendConfirm.sub.resumeAt');
  assert.equal(ahead.when.text, '2026-11-20 (금) 09:00');
  assert.deepEqual(ahead.until, { days: 0, hours: 12, minutes: 0 });
  const passed = model({ mode: 'resume', remaining: 12, sendAt: '2026-11-19T10:00:00Z' });
  assert.equal(passed.title.key, 'campaigns.sendConfirm.title.resume');
  assert.equal(passed.until, null);
  assert.equal(model({ mode: 'resume', remaining: 12 }).when, null);
});

test('nobody: a regular campaign cannot be confirmed, the reason leads the checks', () => {
  const m = model({ recipients: 0, checks: [{ key: 'recipients.zero', level: 'block' }, { key: 'from.placeholder', level: 'ok' }] });
  assert.equal(m.disabled, true);
  assert.equal(m.title.key, 'campaigns.sendConfirm.title.zero');
  assert.equal(m.sub.key, 'campaigns.sendConfirm.sub.blocked');
  assert.deepEqual(m.checks.map((c) => `${c.level}:${c.name}`), ['block:recipients.zero'], 'the reason is the only line');
  assert.equal(m.zero, true);
});

test('a blocked window keeps the plain button text: there is nothing to go ahead anyway with', () => {
  const m = model({ recipients: 0, checks: [{ key: 'recipients.zero', level: 'block' }, { key: 'from.placeholder', level: 'warn' }] });
  assert.equal(m.disabled, true);
  assert.equal(m.confirm.key, 'campaigns.sendConfirm.confirm.start');
});

test('no list left: its own title', () => {
  const m = model({ recipients: 0, lists: [], checks: [{ key: 'lists.none', level: 'block' }] });
  assert.equal(m.disabled, true);
  assert.equal(m.title.key, 'campaigns.sendConfirm.title.noList');
});

test('an opt-in campaign or a resume with nobody only warns, with its own text', () => {
  let m = model({ recipients: 0, checks: [{ key: 'recipients.zero', level: 'warn' }] });
  assert.equal(m.disabled, false);
  assert.equal(m.checks.find((c) => c.name === 'recipients.zero').key, 'campaigns.sendConfirm.check.recipients.zero.optin');
  m = model({ mode: 'resume', remaining: 0, checks: [{ key: 'recipients.zero', level: 'warn' }] });
  assert.equal(m.disabled, false);
  assert.equal(m.checks.find((c) => c.name === 'recipients.zero').key, 'campaigns.sendConfirm.check.recipients.zero.resume');
});

test('check lines: blocking first, warnings, then the passed ones; server order kept within a level', () => {
  const lines = checkLines(pf({
    checks: [
      { key: 'from.placeholder', level: 'ok' },
      { key: 'schedule.past', level: 'warn' },
      { key: 'recipients.zero', level: 'block' },
      { key: 'recent.notApplied', level: 'warn', params: { days: 7, excluded: 0 } },
      { key: 'unsubscribe.missing', level: 'ok' },
    ],
    tracking: { ...trackingOk, state: 'off' },
  }));
  assert.deepEqual(lines.map((l) => `${l.level}:${l.name}`), [
    'block:recipients.zero',
    'warn:schedule.past', 'warn:recent.notApplied', 'warn:tracking',
    'ok:from.placeholder', 'ok:unsubscribe.missing',
  ]);
  // The tracking text is the one of the tracking check (LST-44).
  assert.equal(lines.find((l) => l.name === 'tracking').key, 'campaigns.tracking.confirm.off');
});

test('tracking: ok adds a passed line, a missing pixel and untracked links are two warnings, a failed check warns', () => {
  assert.equal(checkLines(pf()).find((l) => l.name === 'tracking').key, 'campaigns.sendConfirm.check.tracking.ok');
  const two = checkLines(pf({ tracking: { ...trackingOk, state: 'warn', view: 'missing', links: { ...trackingOk.links, untracked: 3 } } })).filter((l) => l.name === 'tracking');
  assert.deepEqual(two.map((l) => l.key), ['campaigns.tracking.confirm.viewMissing', 'campaigns.tracking.confirm.untracked']);
  assert.deepEqual(two[1].params, { n: 3 });
  // What is not tracked is not said to be: plain text has no views, no links no clicks.
  const track = (over) => checkLines(pf({ tracking: { ...trackingOk, ...over } })).filter((l) => l.name === 'tracking').map((l) => `${l.level}:${l.key.split('.').pop()}`);
  assert.deepEqual(track({ view: 'unsupported', links: { ...trackingOk.links, total: 0 } }), ['info:viewUnsupported']);
  assert.deepEqual(track({ view: 'unsupported', links: { ...trackingOk.links, total: 2, manual: 2 } }), ['ok:clickOnly', 'info:viewUnsupported']);
  assert.deepEqual(track({ view: 'manual', links: { ...trackingOk.links, total: 0 } }), ['ok:viewOnly']);
  assert.deepEqual(track({ view: 'auto' }), ['ok:ok']);
  assert.deepEqual(track({ view: 'n/a', applicable: false }), ['info:notApplicable']);
  assert.deepEqual(track({ state: 'off', view: 'off' }), ['warn:off']);
  const failed = checkLines(pf({ tracking: null, trackingError: 'boom' })).find((l) => l.name === 'tracking');
  assert.equal(failed.level, 'warn');
  assert.equal(failed.key, 'campaigns.sendConfirm.check.tracking.failed');
});

test('the recent rule line says the number only when there is one; the unsubscribe line carries the tag', () => {
  const a = checkLines(pf({ checks: [{ key: 'recent.notApplied', level: 'warn', params: { days: 14, excluded: 42 } }] }))[0];
  assert.equal(a.key, 'campaigns.sendConfirm.check.recent.notApplied.warn');
  const b = checkLines(pf({ checks: [{ key: 'recent.notApplied', level: 'warn', params: { days: 14 } }] }))[0];
  assert.equal(b.key, 'campaigns.sendConfirm.check.recent.notApplied.noCount');
  const c = checkLines(pf({ checks: [{ key: 'unsubscribe.missing', level: 'warn' }] }))[0];
  assert.equal(c.params.tag, '{{ UnsubscribeURL }}');
});

test('rich parts: bold and code marks', () => {
  assert.deepEqual(richParts('a **b** c `d`'), [
    { text: 'a ' }, { text: 'b', bold: true }, { text: ' c ' }, { text: 'd', code: true },
  ]);
  assert.deepEqual(richParts('plain'), [{ text: 'plain' }]);
});

test('every i18n key the window can use exists in Korean and English, with the same parameters', () => {
  const read = (l) => JSON.parse(fs.readFileSync(new URL(`../../i18n/${l}.json`, import.meta.url), 'utf8'));
  const ko = read('ko');
  const en = read('en');
  const keys = Object.keys(ko).filter((k) => k.startsWith('campaigns.sendConfirm.'));
  assert.ok(keys.length > 40);
  const params = (s) => [...s.matchAll(/\{(\w+)\}/g)].map((m) => m[1]).sort().join(',');
  keys.forEach((k) => {
    assert.ok(en[k], `${k} is missing in English`);
    assert.equal(params(en[k]), params(ko[k]), `${k}: parameters differ`);
  });
  // The keys the model returns.
  const used = new Set();
  const take = (i) => used.add(i.key);
  [
    pf(), pf({ mode: 'schedule', sendAt: '2026-11-20T00:00:00Z' }), pf({ mode: 'resume' }),
    pf({ recipients: 0, checks: [{ key: 'recipients.zero', level: 'block' }, { key: 'lists.none', level: 'block' }] }),
    pf({ checks: [{ key: 'from.placeholder', level: 'warn' }, { key: 'from.placeholder', level: 'ok' }, { key: 'schedule.past', level: 'warn' }, { key: 'unsubscribe.missing', level: 'warn' }, { key: 'unsubscribe.missing', level: 'ok' }, { key: 'recent.notApplied', level: 'warn', params: { days: 7 } }, { key: 'recent.notApplied', level: 'warn', params: { days: 7, excluded: 1 } }, { key: 'recipients.zero', level: 'warn' }], tracking: null }),
  ].forEach((p) => {
    const m = sendConfirmModel(p, { now: NOW, tz: 'UTC' });
    [m.title, m.sub, m.confirm].forEach(take);
    m.checks.forEach(take);
  });
  used.forEach((k) => assert.ok(ko[k] && en[k], `${k} is not in the language files`));
});
