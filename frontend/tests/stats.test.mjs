import test from 'node:test';
import assert from 'node:assert/strict';
import {
  aggregate, change, changeText, classify, formatKST, groupBy, hourBandOf, lengthBandOf, listGroups, medianOf,
  overlapRows, recipientRows, teamMedian, teamRows, weekdayOf, clickRate, bounceOver, complaintOver, campaignSummary, followupPairs, NO_TEAM_KEY,
} from '../src/stats.js';
import { readFileSync } from 'node:fs';
import { DEFINITIONS, MIN, helpText } from '../src/stats-definitions.js';

const en = JSON.parse(readFileSync(new URL('../../i18n/en.json', import.meta.url)));
const t = (key, params = {}) => (en[key] || key).replace(/\{(\w+)\}/g, (_, k) => params[k]);

const NOW = new Date('2026-11-25T00:05:00Z'); // 09:05 KST on a Wednesday
const day = (n) => new Date(NOW.getTime() - n * 86400000).toISOString();
let id = 0;
const camp = (o = {}) => ({
  id: (id += 1),
  name: `c${id}`,
  subject: 'subject',
  status: 'finished',
  startedAt: day(10),
  sent: 1000,
  viewers: 400,
  clickers: 80,
  hard: 10,
  soft: 10,
  complaint: 0,
  ledgerRows: 1000,
  overlap30: 0,
  trackingOff: false,
  teamTags: ['팀:가'],
  own: true,
  lists: [{ id: 1, name: 'L1' }],
  listsN: 1,
  ...o,
});

test('a campaign counts in the rates only when finished, tracked and three days old', () => {
  assert.equal(classify(camp(), NOW).rateOk, true);
  const young = classify(camp({ startedAt: day(1) }), NOW);
  assert.equal(young.collecting, true);
  assert.equal(young.rateOk, false);
  assert.equal(young.bounceOk, false);
  assert.equal(classify(camp({ status: 'running' }), NOW).rateOk, false);
  assert.equal(classify(camp({ status: 'cancelled' }), NOW).state, 'cancelled');
  const untracked = classify(camp({ trackingOff: true }), NOW);
  assert.equal(untracked.untracked, true);
  assert.equal(untracked.rateOk, false);
  assert.equal(untracked.bounceOk, true, 'bounces do not depend on tracking');
});

test('zero opens and clicks is not tracked, but zero clicks with recorded opens is a real zero', () => {
  const c = camp({ clickers: 0, viewers: 0 });
  assert.equal(classify(c, NOW).untracked, true);
  assert.equal(aggregate([c], NOW).click, null);
  assert.equal(aggregate([c], NOW).open, null);
  assert.equal(aggregate([c], NOW).nRate, 0);
  const tracked = camp({ clickers: 0 });
  assert.equal(classify(tracked, NOW).untracked, false);
  assert.equal(aggregate([tracked], NOW).click, 0);
});

test('aggregate: rates use the campaigns that count, sent counts every finished campaign', () => {
  const list = [
    camp({ sent: 100, clickers: 10, viewers: 40, hard: 1, soft: 1, complaint: 1 }),
    camp({ sent: 900, clickers: 90, viewers: 360, hard: 9, soft: 9 }),
    camp({ sent: 500, startedAt: day(1), clickers: 400 }), // collecting
    camp({ sent: 500, status: 'running', clickers: 400 }),
    camp({ sent: 300, trackingOff: true, clickers: 0, viewers: 0, hard: 3, soft: 0 }),
  ];
  const a = aggregate(list, NOW);
  assert.equal(a.n, 4, 'the running campaign is not counted');
  assert.equal(a.sent, 100 + 900 + 500 + 300);
  assert.equal(a.nRate, 2);
  assert.equal(a.click, 10); // 100 / 1000
  assert.equal(a.open, 40);
  assert.equal(a.sBounce, 1300);
  assert.equal(a.bounces, 2 + 18 + 3);
  assert.equal(a.complaints, 1);
  assert.equal(a.complaint.toFixed(2), '0.08');
  assert.equal(a.collecting, 1);
  assert.equal(a.untracked, 1);
});

test('the hard and soft bounce and the complaint are shown apart (proof summary bounce = bounce + complaint)', () => {
  const a = aggregate([camp({ sent: 100, hard: 1, soft: 1, complaint: 1 })], NOW);
  assert.equal(a.bounces, 2);
  assert.equal(a.complaints, 1);
});

test('warnings compare raw counts and only apply at the metric’s minimum sample', () => {
  assert.equal(bounceOver(200, 10000), false);
  assert.equal(complaintOver(10, 10000), false);
  const above = campaignSummary(camp({ sent: 10000, hard: 204, soft: 0 }), [], NOW);
  assert.equal(above.bounce.toFixed(1), '2.0');
  assert.equal(above.bounceWarning, true);
  const complaint = campaignSummary(camp({ sent: 9900, complaint: 10 }), [], NOW);
  assert.equal(complaint.complaint.toFixed(2), '0.10');
  assert.equal(complaint.complaintWarning, true);
  const small = campaignSummary(camp({ sent: 71, hard: 3, soft: 0, complaint: 1 }), [], NOW);
  assert.equal(small.bounceWarning, false);
  assert.equal(small.complaintWarning, false);
  assert.equal(campaignSummary(camp({ sent: 200, complaint: 1 }), [], NOW).complaintWarning, false);
});

test('median needs three campaigns that count; small and collecting ones are left out', () => {
  assert.equal(medianOf([3, 1, 2]), 2);
  assert.equal(medianOf([1, 2, 3, 4]), 2.5);
  const two = [camp({ clickers: 10 }), camp({ clickers: 20 })];
  assert.equal(teamMedian([...two, camp({ sent: 199, clickers: 199 })], NOW), null);
  const three = [...two, camp({ clickers: 30 }), camp({ clickers: 999, startedAt: day(1) })];
  assert.deepEqual(teamMedian(three, NOW), { value: 2, n: 3 });
});

test('a change inside the neutral width is similar and a falling bounce rate is good', () => {
  assert.equal(change(0.4, 'click').dir, 'same');
  assert.equal(change(0.5, 'click').dir, 'up');
  assert.equal(change(-1.2, 'click').good, false);
  assert.equal(change(1.2, 'click').good, true);
  assert.equal(change(-0.5, 'bounce').good, true);
  assert.equal(change(0.2, 'bounce').dir, 'same');
  assert.match(changeText(change(0.9, 'click'), t), /▲ \+0\.9%p/);
  assert.match(changeText(change(0.1, 'click'), t), /Similar/);
});

test('team rows: every team role is a row, one that sent nothing is empty, small teams come last', () => {
  const camps = [
    ...[1, 2, 3].map(() => camp({ teamTags: ['팀:가'], sent: 1000, clickers: 100 })),
    camp({ teamTags: ['팀:나'], sent: 71, clickers: 8 }),
    camp({ teamTags: ['팀:가', '팀:나'], sent: 1000, clickers: 50 }),
    camp({ teamTags: [], sent: 1000, clickers: 10 }),
  ];
  const rows = teamRows(camps, [{ tag: '팀:가', lists: 2 }, { tag: '팀:나', lists: 1 }, { tag: '팀:다', lists: 3 }], NOW);
  const by = Object.fromEntries(rows.map((r) => [r.tag, r]));
  assert.equal(by['팀:가'].a.n, 4, 'a campaign of two teams counts in both');
  assert.equal(by['팀:나'].a.n, 2);
  assert.equal(by['팀:나'].a.enough, false);
  assert.equal(by['팀:다'].empty, true);
  assert.equal(rows[rows.length - 1].tag, '팀:다', 'a team that sent nothing is last');
  assert.ok(by[NO_TEAM_KEY], 'a campaign without a team makes the no-team row');
  assert.equal(rows[0].tag, '팀:가', 'the team with a big enough sample comes first');
});

test('groups: a group needs three campaigns and 1,000 messages, and the best needs two that qualify', () => {
  assert.equal(MIN.groupCampaigns, 3);
  const mon = new Date('2026-11-16T01:00:00Z').toISOString(); // Monday 10:00 KST
  const tue = new Date('2026-11-17T01:00:00Z').toISOString(); // Tuesday 10:00 KST
  const wed = new Date('2026-11-18T09:00:00Z').toISOString(); // Wednesday 18:00 KST
  assert.equal(weekdayOf({ startedAt: mon }), 0);
  assert.equal(weekdayOf({ startedAt: tue }), 1);
  assert.equal(hourBandOf({ startedAt: mon }), 1);
  assert.equal(hourBandOf({ startedAt: wed }), 3);
  const camps = [
    ...[1, 2, 3].map(() => camp({ startedAt: mon, clickers: 100 })),
    ...[1, 2, 3].map(() => camp({ startedAt: tue, clickers: 50 })),
    camp({ startedAt: wed, clickers: 900 }),
  ];
  const g = groupBy(camps, ['월', '화', '수'], (c) => weekdayOf(c), NOW);
  assert.deepEqual(g.ok.map((r) => r.label), ['월', '화']);
  assert.equal(g.best.label, '월');
  assert.equal(g.rows[2].ok, false, 'one campaign is a small sample');
  const lone = groupBy(camps.slice(0, 3), ['월', '화'], (c) => weekdayOf(c), NOW);
  assert.equal(lone.best, null, 'no best with one group that qualifies');
});

test('subject length bands count characters, not bytes', () => {
  assert.equal(lengthBandOf({ subject: '가'.repeat(20) }), 0);
  assert.equal(lengthBandOf({ subject: '가'.repeat(21) }), 1);
  assert.equal(lengthBandOf({ subject: '가'.repeat(41) }), 2);
});

test('list groups name only lists of the caller; the others are merged', () => {
  const camps = [
    camp({ lists: [{ id: 1, name: '뉴스레터' }], clickers: 100 }),
    camp({ lists: [{ id: 1, name: '뉴스레터' }], clickers: 90 }),
    camp({ lists: [{ id: 2, name: '설문' }], clickers: 50 }),
    camp({ own: false, lists: undefined, clickers: 300 }),
    camp({ listsN: 2, lists: [{ id: 3, name: 'A' }, { id: 4, name: 'B' }], clickers: 10 }),
  ];
  const g = listGroups(camps, NOW);
  assert.deepEqual(g.rows.filter((r) => !r.hidden).filter((r) => r.list).map((r) => r.label).sort(), ['뉴스레터', '설문']);
  const hidden = g.rows.find((r) => r.hidden);
  assert.equal(hidden.a.n, 1);
  assert.ok(g.rows.find((r) => r.key === 'multiple'));
  assert.equal(g.best.label, '뉴스레터', 'the hidden group is never named best');
  assert.ok(!JSON.stringify(g).includes('"name":"B"'));
});

test('overlap uses separate per-campaign counts for the campaigns in view', () => {
  const a = camp();
  const b = camp();
  const o = overlapRows([a, b], [
    { id: a.id, ledgerRows: 100, overlap7: 5, overlap30: 10 },
    { id: 1000, ledgerRows: 200, overlap7: 100, overlap30: 200 },
  ]);
  assert.equal(o.rows.length, 1);
  assert.equal(o.overlap30, 10);
  assert.equal(o.rate, 10);
  assert.equal(o.recentRate, 5);
});

test('recipient groups: rates per group, the merged other group last', () => {
  const groups = [
    { team: '', key: 'KR', sent: 2000, recipients: 200, opened: 800, clicked: 160, bounced: 20 },
    { team: '', key: '__other__', sent: 25, recipients: 20, opened: 5, clicked: 1, bounced: 0 },
    { team: '', key: 'JP', sent: 100, recipients: 90, opened: 40, clicked: 5, bounced: 1 },
    { team: '팀:가', key: 'KR', sent: 10, recipients: 5, opened: 1, clicked: 0, bounced: 0 },
  ];
  const rows = recipientRows(groups, '');
  assert.deepEqual(rows.map((r) => r.key), ['KR', 'JP', '__other__']);
  assert.equal(rows[0].click, 8);
  assert.equal(rows[1].ok, false);
});

test('KST date text does not depend on the zone of the machine', () => {
  assert.equal(formatKST('2026-11-23T16:05:00Z', 'ko'), '11-24(화) 01:05');
});

test('every figure the screen explains has a definition with a meaning, a formula and a sample', () => {
  ['sent', 'click', 'open', 'bounce', 'complaint', 'neutral', 'small', 'collecting', 'untracked', 'median'].forEach((k) => {
    const d = DEFINITIONS.find((x) => x.key === k);
    assert.ok(d, k);
    assert.ok(d.name && d.meaning && d.formula && d.compare && d.sample, k);
    assert.ok(helpText(k, t).includes(t(d.formula)));
  });
  assert.equal(clickRate({ clickers: 1, sent: 8 }), 12.5);
});

test('source tags group different lists together and same-name untagged lists stay separate', () => {
  const lists = [
    { id: 1, name: 'Same' }, { id: 2, name: 'Same' },
    { id: 3, name: 'First', source: 'Webinar' }, { id: 4, name: 'Second', source: 'Webinar' },
  ];
  const { rows } = listGroups(lists.map((list) => camp({ lists: [list] })), NOW);
  assert.deepEqual(rows.map((r) => [r.key, r.a.sent, r.a.bounce]), [
    ['list:1', 1000, 2], ['list:2', 1000, 2], ['source:Webinar', 2000, 2],
  ]);
});

test('peer comparison excludes the current campaign and falls back to company peers', () => {
  const current = camp({ clickers: 100 });
  const peers = [camp({ clickers: 20 }), camp({ clickers: 40 })];
  assert.equal(campaignSummary(current, [current, ...peers], NOW).median, null);
  const company = camp({ teamTags: ['팀:나'], clickers: 60 });
  const summary = campaignSummary(current, [current, ...peers, company], NOW);
  assert.equal(summary.team, '');
  assert.deepEqual(summary.median, { value: 4, n: 3 });
});

test('a follow-up shows combined unique people over original sent even outside the current range', () => {
  const child = camp({
    followupOf: 900, followupParentSent: 2000, combinedClickers: 120, combinedViewers: 550,
    followupCondition: { open: 'any', click: 'any' },
  });
  const [pair] = followupPairs([child], NOW);
  assert.equal(pair.click, 6);
  assert.equal(pair.open.toFixed(1), '27.5');
  assert.equal(pair.followup.followupCondition.click, 'any');
});
