// The proof summary block v1 (LST-36) is pinned here, line by line: a later reader (AXIOM)
// depends on it. Change the format only with a new header (v2) and these tests.
import test from 'node:test';
import assert from 'node:assert/strict';
import {
  DEFINITION_LINE, PROOF_SUMMARY_HEADER, buildProofSummaryV1, formatKST, parseProofSummaryV1,
  teamsOfCampaign, trackingStateFor,
} from '../src/proof-summary.js';

// 2026-11-19 15:30 KST = 06:30 UTC. Created 2026-11-12 10:12 KST.
const AT = new Date('2026-11-19T06:30:00Z');
const CREATED = '2026-11-12T01:12:00Z';
const UUID = '4d9b346a-2085-4d49-9257-fca74821f256';
const CAMPAIGN = {
  id: 52, name: '북미 틱톡 어필리에이트', uuid: UUID, createdAt: CREATED,
};
const FROM = Math.floor(new Date(CREATED).getTime() / 1000);
const TO = Math.floor(AT.getTime() / 1000);

const base = (over = {}) => ({
  campaign: CAMPAIGN,
  teams: ['마케팅-2팀'],
  sent: 15390,
  views: 1664,
  clicks: 27,
  bounces: 392,
  tracking: true,
  at: AT,
  rootURL: 'https://lists.crevasse.ai',
  ...over,
});

const linkOf = (id = 52, from = FROM, to = TO) => `확인 링크: https://lists.crevasse.ai/admin/campaigns/analytics?id=${id}&from=${from}&to=${to} (listmonk 권한 필요)`;

test('the block is the LAB-298 example, line for line (only the link carries the range)', () => {
  const want = [
    '[listmonk 집계 v1]',
    '기준 시각: 2026-11-19 15:30 KST',
    '캠페인: #52 · 북미 틱톡 어필리에이트 (팀: 마케팅-2팀)',
    `캠페인 UUID: ${UUID}`,
    '발송 15,390 · 열람 1,664 (10.81%) · 클릭 27 (0.18%) · 반송 392 (2.55%)',
    '정의: 발송=listmonk 발송 카운터(수신함 도착이 아님) · 열람/클릭=고유 수신자 수 · 반송=반송 기록 행 수 · 비율의 분모=발송',
    '회신: 집계 전',
    '포함 안 함: 이름·이메일 주소·본문·개인별 활동',
    linkOf(),
  ].join('\n');
  assert.equal(buildProofSummaryV1(base()), want);
});

test('nine lines, fixed headers in a fixed order, no trailing newline', () => {
  const text = buildProofSummaryV1(base());
  const lines = text.split('\n');
  assert.equal(lines.length, 9);
  assert.equal(text.endsWith('\n'), false);
  const heads = ['[listmonk 집계 v1]', '기준 시각: ', '캠페인: #', '캠페인 UUID: ', '발송 ', '정의: ', '회신: 집계 전', '포함 안 함: ', '확인 링크: '];
  heads.forEach((h, i) => assert.ok(lines[i].startsWith(h), `line ${i + 1} starts with ${h}`));
  assert.equal(lines[0], PROOF_SUMMARY_HEADER);
  assert.equal(lines[5], DEFINITION_LINE);
});

test('individual tracking off or all tracking off: views and clicks read "추적 없음", bounces stay numbers', () => {
  const text = buildProofSummaryV1(base({
    sent: 30, views: 0, clicks: 0, bounces: 2, tracking: trackingStateFor({
      privacy: { disable_tracking: false, individual_tracking: false }, sent: 30, viewsTotal: 12, clicksTotal: 3,
    }),
  }));
  assert.equal(text.split('\n')[4], '발송 30 · 열람 추적 없음 · 클릭 추적 없음 · 반송 2 (6.67%)');
  assert.equal(trackingStateFor({
    privacy: { disable_tracking: true, individual_tracking: true }, sent: 30, viewsTotal: 12, clicksTotal: 3,
  }), false);
  assert.equal(trackingStateFor({ privacy: undefined, sent: 30, viewsTotal: 12, clicksTotal: 3 }), false);
});

test('a campaign that went out with no open and no click record at all has no tracking', () => {
  const privacy = { disable_tracking: false, individual_tracking: true };
  assert.equal(trackingStateFor({
    privacy, sent: 100, viewsTotal: 0, clicksTotal: 0,
  }), false);
  // One of them is enough to read the zero of the other as a zero.
  assert.equal(trackingStateFor({
    privacy, sent: 100, viewsTotal: 4, clicksTotal: 0,
  }), true);
  assert.equal(trackingStateFor({
    privacy, sent: 100, viewsTotal: 0, clicksTotal: 1,
  }), true);
  // Nothing sent: nothing to read as "no tracking".
  assert.equal(trackingStateFor({
    privacy, sent: 0, viewsTotal: 0, clicksTotal: 0,
  }), true);
});

test('nothing sent: the ratio is "(-)", never a division', () => {
  const text = buildProofSummaryV1(base({
    sent: 0, views: 0, clicks: 0, bounces: 0,
  }));
  assert.equal(text.split('\n')[4], '발송 0 · 열람 0 (-) · 클릭 0 (-) · 반송 0 (-)');
});

test('teams: none, one, two (by name, no repeats)', () => {
  assert.equal(buildProofSummaryV1(base({ teams: [] })).split('\n')[2], '캠페인: #52 · 북미 틱톡 어필리에이트 (팀: 없음)');
  assert.equal(buildProofSummaryV1(base({ teams: ['가팀', '나팀'] })).split('\n')[2], '캠페인: #52 · 북미 틱톡 어필리에이트 (팀: 가팀, 나팀)');
  const known = [
    { id: 1, tags: ['팀:나팀', '북미'] },
    { id: 2, tags: ['팀:가팀', '팀:나팀'] },
    { id: 3, tags: ['북미'] },
  ];
  assert.deepEqual(teamsOfCampaign([{ id: 1 }, { id: 2 }, { id: 3 }], known), ['가팀', '나팀']);
  assert.deepEqual(teamsOfCampaign([{ id: 3 }], known), []);
  // A list the screen does not know adds nothing; no lists at all is no team.
  assert.deepEqual(teamsOfCampaign([{ id: 9 }], known), []);
  assert.deepEqual(teamsOfCampaign(undefined, undefined), []);
});

test('a name with line breaks and runs of spaces stays on one line', () => {
  const text = buildProofSummaryV1(base({ campaign: { ...CAMPAIGN, name: '  북미 \n 틱톡\r\n\t어필리에이트  ' } }));
  assert.equal(text.split('\n').length, 9);
  assert.equal(text.split('\n')[2], '캠페인: #52 · 북미 틱톡 어필리에이트 (팀: 마케팅-2팀)');
});

test('the time is Korean time, whatever the time zone of the machine', () => {
  // 15:00 UTC is midnight of the next day in Korea.
  assert.equal(formatKST(new Date('2026-11-19T15:00:00Z')), '2026-11-20 00:00 KST');
  assert.equal(formatKST(new Date('2026-11-19T14:59:59Z')), '2026-11-19 23:59 KST');
  assert.equal(formatKST(new Date('2026-12-31T15:00:00Z')), '2027-01-01 00:00 KST');
  assert.equal(formatKST(new Date('2026-01-05T00:05:00Z')), '2026-01-05 09:05 KST');
  const was = process.env.TZ;
  try {
    ['America/Los_Angeles', 'UTC', 'Asia/Tokyo'].forEach((tz) => {
      process.env.TZ = tz;
      assert.equal(formatKST(AT), '2026-11-19 15:30 KST', tz);
    });
  } finally {
    if (was === undefined) delete process.env.TZ; else process.env.TZ = was;
  }
});

test('rounding: two places, a third of a thing is 33.33% and two thirds 66.67%', () => {
  const line = (sent, views, clicks, bounces) => buildProofSummaryV1(base({
    sent, views, clicks, bounces,
  })).split('\n')[4];
  assert.equal(line(3, 1, 2, 0), '발송 3 · 열람 1 (33.33%) · 클릭 2 (66.67%) · 반송 0 (0.00%)');
  assert.equal(line(8, 1, 7, 8), '발송 8 · 열람 1 (12.50%) · 클릭 7 (87.50%) · 반송 8 (100.00%)');
  assert.equal(line(1000000, 1, 999999, 0), '발송 1,000,000 · 열람 1 (0.00%) · 클릭 999,999 (100.00%) · 반송 0 (0.00%)');
});

test('the link carries the whole life of the campaign and the time of the numbers', () => {
  const text = buildProofSummaryV1(base({ rootURL: 'https://lists.crevasse.ai/' }));
  assert.equal(text.split('\n')[8], linkOf());
  const other = buildProofSummaryV1(base({ campaign: { ...CAMPAIGN, id: 7 }, at: new Date('2026-11-20T00:00:00Z') }));
  assert.equal(other.split('\n')[8], linkOf(7, FROM, Math.floor(new Date('2026-11-20T00:00:00Z').getTime() / 1000)));
});

test('build then read gives back the same values', () => {
  const got = parseProofSummaryV1(buildProofSummaryV1(base({ teams: ['가팀', '나팀'] })));
  assert.deepEqual(got, {
    at: '2026-11-19 15:30',
    id: 52,
    name: '북미 틱톡 어필리에이트',
    teams: ['가팀', '나팀'],
    uuid: UUID,
    sent: 15390,
    views: { tracked: true, count: 1664, percent: 10.81 },
    clicks: { tracked: true, count: 27, percent: 0.18 },
    bounces: { count: 392, percent: 2.55 },
    link: {
      url: `https://lists.crevasse.ai/admin/campaigns/analytics?id=52&from=${FROM}&to=${TO}`, id: 52, from: FROM, to: TO,
    },
  });

  const none = parseProofSummaryV1(buildProofSummaryV1(base({ teams: [], sent: 0, tracking: false })));
  assert.deepEqual(none.views, { tracked: false });
  assert.deepEqual(none.clicks, { tracked: false });
  assert.deepEqual(none.bounces, { count: 392, percent: null });
  assert.deepEqual(none.teams, []);

  const odd = parseProofSummaryV1(buildProofSummaryV1(base({ campaign: { ...CAMPAIGN, name: '봄 (팀: 시험) · 특가' }, teams: ['가팀'] })));
  assert.equal(odd.name, '봄 (팀: 시험) · 특가');
  assert.deepEqual(odd.teams, ['가팀']);
});

test('text that is not a v1 block reads as null', () => {
  const good = buildProofSummaryV1(base());
  assert.notEqual(parseProofSummaryV1(good), null);
  assert.equal(parseProofSummaryV1(''), null);
  assert.equal(parseProofSummaryV1(undefined), null);
  assert.equal(parseProofSummaryV1(`${good}\n`), null);
  assert.equal(parseProofSummaryV1(good.replace('[listmonk 집계 v1]', '[listmonk 집계 v2]')), null);
  assert.equal(parseProofSummaryV1(good.replace('회신: 집계 전', '회신: 12')), null);
  assert.equal(parseProofSummaryV1(good.replace('반송 392', '반송 x')), null);
  assert.equal(parseProofSummaryV1(good.split('\n').slice(0, 8).join('\n')), null);
});

test('no personal data: an email, a subject or a body in the inputs never reaches the text', () => {
  const text = buildProofSummaryV1(base({
    campaign: {
      ...CAMPAIGN,
      subject: '개인 제목 SECRET-SUBJECT',
      body: '<p>SECRET-BODY hello@example.test</p>',
      fromEmail: 'noreply@example.test',
      email: 'someone@example.test',
      subscribers: [{ email: 'sub@example.test', name: 'SECRET-NAME' }],
    },
    subscribers: ['sub@example.test'],
    topLinks: ['https://example.test/?e=sub@example.test'],
  }));
  assert.equal(text.includes('@'), false);
  ['SECRET-SUBJECT', 'SECRET-BODY', 'SECRET-NAME', 'example.test'].forEach((s) => assert.equal(text.includes(s), false, s));
  assert.equal(text.split('\n').length, 9);
});
