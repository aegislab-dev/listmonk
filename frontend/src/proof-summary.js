// Proof summary block v1 (LST-36): the text the campaign analytics screen copies so a person
// can paste a campaign's counts into a proof. The format is fixed on purpose: a later reader
// (AXIOM) parses it, and tests/proof-summary.test.mjs pins every line. To change the format,
// raise the header to v2 and change the tests with it.
//
// Plain functions, no browser or Vue. The text is Korean whatever language the screen has.
import { teamName, teamTags } from './teams.js'; // eslint-disable-line import/extensions

export const PROOF_SUMMARY_HEADER = '[listmonk 집계 v1]';
export const DEFINITION_LINE = '정의: 발송=listmonk 발송 카운터(수신함 도착이 아님) · 열람/클릭=고유 수신자 수 · 반송=반송 기록 행 수 · 비율의 분모=발송';
export const REPLY_LINE = '회신: 집계 전';
export const EXCLUDED_LINE = '포함 안 함: 이름·이메일 주소·본문·개인별 활동';
export const LINK_SUFFIX = '(listmonk 권한 필요)';
export const NO_TRACKING = '추적 없음';
export const NO_TEAM_NAME = '없음';

const pad = (n) => String(n).padStart(2, '0');

// "2026-11-19 15:30 KST" for a Date, whatever the time zone of the browser or the machine.
export function formatKST(date) {
  const parts = new Intl.DateTimeFormat('en-CA', {
    timeZone: 'Asia/Seoul',
    hourCycle: 'h23',
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
  }).formatToParts(date).reduce((o, p) => ({ ...o, [p.type]: p.value }), {});
  return `${parts.year}-${parts.month}-${parts.day} ${pad(parts.hour)}:${parts.minute} KST`;
}

// The team names of a campaign: the team tags of its lists (a campaign's own `lists` carry no
// tags, so the tags come from `knownLists`, the lists the screen knows), without repeats, in
// name order. A list the screen does not know adds nothing.
export function teamsOfCampaign(campaignLists, knownLists) {
  const byID = new Map((knownLists || []).map((l) => [l.id, l]));
  const names = new Set();
  (campaignLists || []).forEach((cl) => {
    const known = byID.get(cl.id);
    teamTags(known ? known.tags : cl.tags).forEach((t) => names.add(teamName(t)));
  });
  return [...names].sort();
}

// Whether the opens and clicks of a campaign can be told. Two rules (decided with LST-36):
// tracking is off in the settings (everything, or per subscriber: then the counts are rows,
// not people), or the campaign went out and has no open and no click record at all in its
// whole life (a campaign sent without the tracking tags reads as zero, not as "nobody
// opened it"). The totals are the ones over the campaign's whole life.
export function trackingStateFor({
  privacy, sent, viewsTotal, clicksTotal,
}) {
  if (!privacy || privacy.disable_tracking || !privacy.individual_tracking) {
    return false;
  }
  if (sent > 0 && viewsTotal === 0 && clicksTotal === 0) {
    return false;
  }
  return true;
}

const num = (n) => Number(n).toLocaleString('en-US');
const pct = (n, sent) => (sent > 0 ? `${((n / sent) * 100).toFixed(2)}%` : '-');

// One name on one line: line breaks and runs of spaces become one space.
export const oneLine = (s) => String(s === undefined || s === null ? '' : s).replace(/\s+/g, ' ').trim();

const unix = (d) => Math.floor(new Date(d).getTime() / 1000);

// The block. campaign = { id, name, uuid, createdAt }; teams = names; sent/views/clicks/bounces
// = numbers; tracking = whether views and clicks are shown; at = Date of the numbers;
// rootURL = the root URL of the listmonk. Only these fields are read, nothing else of a
// campaign gets into the text.
export function buildProofSummaryV1({
  campaign, teams, sent, views, clicks, bounces, tracking, at, rootURL,
}) {
  const team = teams && teams.length > 0 ? teams.join(', ') : NO_TEAM_NAME;
  const stat = (label, n) => `${label} ${num(n)} (${pct(n, sent)})`;
  const link = `${String(rootURL || '').replace(/\/+$/, '')}/admin/campaigns/analytics`
    + `?id=${campaign.id}&from=${unix(campaign.createdAt)}&to=${unix(at)} ${LINK_SUFFIX}`;
  return [
    PROOF_SUMMARY_HEADER,
    `기준 시각: ${formatKST(at)}`,
    `캠페인: #${campaign.id} · ${oneLine(campaign.name)} (팀: ${team})`,
    `캠페인 UUID: ${campaign.uuid}`,
    [
      `발송 ${num(sent)}`,
      tracking ? stat('열람', views) : `열람 ${NO_TRACKING}`,
      tracking ? stat('클릭', clicks) : `클릭 ${NO_TRACKING}`,
      stat('반송', bounces),
    ].join(' · '),
    DEFINITION_LINE,
    REPLY_LINE,
    EXCLUDED_LINE,
    `확인 링크: ${link}`,
  ].join('\n');
}

// Reads a block back: the values, or null when the text is not a v1 block. The reader the
// later AXIOM card moves to; the tests check that what is built is what is read.
export function parseProofSummaryV1(text) {
  if (typeof text !== 'string') return null;
  const lines = text.split('\n');
  if (lines.length !== 9 || lines[0] !== PROOF_SUMMARY_HEADER) return null;
  if (lines[5] !== DEFINITION_LINE || lines[6] !== REPLY_LINE || lines[7] !== EXCLUDED_LINE) return null;

  const at = /^기준 시각: (\d{4}-\d{2}-\d{2} \d{2}:\d{2}) KST$/.exec(lines[1]);
  const uuid = /^캠페인 UUID: (\S+)$/.exec(lines[3]);
  const head = /^캠페인: #(\d+) · (.*)$/.exec(lines[2]);
  const link = /^확인 링크: (\S+) \(listmonk 권한 필요\)$/.exec(lines[8]);
  if (!at || !uuid || !head || !link) return null;
  const cut = head[2].lastIndexOf(' (팀: ');
  if (cut < 0 || !head[2].endsWith(')')) return null;
  const teamText = head[2].slice(cut + ' (팀: '.length, -1);

  const parts = lines[4].split(' · ');
  if (parts.length !== 4) return null;
  const count = (p, label) => {
    const m = new RegExp(`^${label} ([\\d,]+) \\((?:(\\d+\\.\\d{2})%|-)\\)$`).exec(p);
    if (!m) return null;
    return { count: parseInt(m[1].replace(/,/g, ''), 10), percent: m[2] === undefined ? null : parseFloat(m[2]) };
  };
  const sent = /^발송 ([\d,]+)$/.exec(parts[0]);
  const untracked = (p, label) => p === `${label} ${NO_TRACKING}`;
  const views = untracked(parts[1], '열람') ? { tracked: false } : count(parts[1], '열람');
  const clicks = untracked(parts[2], '클릭') ? { tracked: false } : count(parts[2], '클릭');
  const bounces = count(parts[3], '반송');
  if (!sent || !views || !clicks || !bounces) return null;

  const linkURL = new URL(link[1]);
  return {
    at: at[1],
    id: parseInt(head[1], 10),
    name: head[2].slice(0, cut),
    teams: teamText === NO_TEAM_NAME ? [] : teamText.split(', '),
    uuid: uuid[1],
    sent: parseInt(sent[1].replace(/,/g, ''), 10),
    views: views.tracked === false ? views : { tracked: true, ...views },
    clicks: clicks.tracked === false ? clicks : { tracked: true, ...clicks },
    bounces,
    link: {
      url: link[1],
      id: parseInt(linkURL.searchParams.get('id'), 10),
      from: parseInt(linkURL.searchParams.get('from'), 10),
      to: parseInt(linkURL.searchParams.get('to'), 10),
    },
  };
}
