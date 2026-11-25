// Statistics dashboard calculations (LST-64). Plain functions over the answer of
// /api/stats/campaigns: rates, what counts in a rate, teams, comparisons and the groups of
// the "for the next send" cards. No Vue, no browser state: tests/stats.test.mjs pins them.
import {
  MIN, SETTLE_DAYS, BASELINE, NEUTRAL,
} from './stats-definitions.js'; // eslint-disable-line import/extensions
import { teamName } from './teams.js'; // eslint-disable-line import/extensions

export const NO_TEAM_KEY = '__none__';
export const NO_TEAM_LABEL = '팀 없음';
const DAY_MS = 24 * 60 * 60 * 1000;

// A percentage with one decimal place, the complaint rate with two.
export const pct = (n, d, digits = 1) => (d > 0 ? Math.round((n / d) * 100 * (10 ** digits)) / (10 ** digits) : 0);
export const fmt = (n) => Number(n).toLocaleString('en-US');
export const pctText = (v, digits = 1) => `${Number(v).toFixed(digits)}%`;

// What a campaign is in the statistics. State: done (finished), running, paused, cancelled.
// settled: three days have passed. rateOk: opens and clicks count (done, tracked, settled);
// bounceOk: bounces count (done, settled). Running, paused, cancelled, collecting and
// untracked campaigns are shown but left out of every rate and median.
export function classify(c, now = new Date()) {
  const state = c.status === 'finished' ? 'done' : c.status;
  const started = c.startedAt ? new Date(c.startedAt).getTime() : 0;
  const age = now.getTime() - started;
  const settled = started > 0 && age >= SETTLE_DAYS * DAY_MS;
  const done = state === 'done';
  return {
    state,
    settled,
    hours: Math.max(0, Math.floor(age / 3600000)),
    collecting: done && !settled,
    untracked: done && !!c.trackingOff,
    rateOk: done && !c.trackingOff && settled && c.sent > 0,
    bounceOk: done && settled && c.sent > 0,
  };
}

// The totals of a set of campaigns: `n` finished campaigns, their `sent`, and the rates with
// the campaigns that count for them.
export function aggregate(camps, now = new Date()) {
  const a = {
    n: 0, sent: 0, nRate: 0, sRate: 0, viewers: 0, clickers: 0, sBounce: 0, bounces: 0, complaints: 0, collecting: 0, untracked: 0,
  };
  camps.forEach((c) => {
    const k = classify(c, now);
    if (k.state !== 'done') {
      return;
    }
    a.n += 1;
    a.sent += c.sent;
    if (k.collecting) {
      a.collecting += 1;
    }
    if (k.untracked) {
      a.untracked += 1;
    }
    if (k.rateOk) {
      a.nRate += 1;
      a.sRate += c.sent;
      a.viewers += c.viewers;
      a.clickers += c.clickers;
    }
    if (k.bounceOk) {
      a.sBounce += c.sent;
      a.bounces += c.hard + c.soft;
      a.complaints += c.complaint;
    }
  });
  a.click = pct(a.clickers, a.sRate);
  a.open = pct(a.viewers, a.sRate);
  a.bounce = pct(a.bounces, a.sBounce);
  a.complaint = pct(a.complaints, a.sBounce, 2);
  a.enough = a.nRate >= MIN.groupCampaigns && a.sRate >= MIN.groupSent;
  return a;
}

// The click rate of one campaign, and the middle value of a team's click rates (null when the
// team has fewer than three campaigns that count).
export const clickRate = (c) => pct(c.clickers, c.sent);
export function medianOf(values) {
  if (values.length === 0) {
    return null;
  }
  const v = [...values].sort((x, y) => x - y);
  const m = Math.floor(v.length / 2);
  return v.length % 2 ? v[m] : Math.round(((v[m - 1] + v[m]) / 2) * 10) / 10;
}
export function teamMedian(camps, now = new Date()) {
  const rates = camps.filter((c) => classify(c, now).rateOk).map(clickRate);
  return rates.length >= MIN.groupCampaigns ? { value: medianOf(rates), n: rates.length } : null;
}

// How a change reads: 'same' inside the neutral width, else up or down; `good` tells whether
// that direction is the better one (a falling bounce rate is). No red or green: the screen
// colors a good change blue and the rest grey.
export function change(delta, kind) {
  const width = NEUTRAL[kind];
  const d = Math.round(delta * 100) / 100;
  if (Math.abs(d) < width) {
    return { dir: 'same', good: false, delta: d };
  }
  const up = d > 0;
  const higherIsBetter = kind === 'click' || kind === 'open';
  return { dir: up ? 'up' : 'down', good: up === higherIsBetter, delta: d };
}
export const changeText = (ch) => {
  const sign = ch.delta > 0 ? '+' : '';
  const v = `${sign}${ch.delta.toFixed(1)}%p`;
  if (ch.dir === 'same') {
    return `≈ 비슷함 (${v})`;
  }
  return `${ch.dir === 'up' ? '▲' : '▼'} ${v}`;
};

// Over the line that the rate is read against.
export const bounceOver = (rate) => rate > BASELINE.bounce;
export const complaintOver = (rate) => rate > BASELINE.complaint;

// The teams of a campaign as tags; a campaign without a team is NO_TEAM_KEY.
export const campaignTeams = (c) => (c.teamTags && c.teamTags.length > 0 ? c.teamTags : [NO_TEAM_KEY]);
export const teamLabel = (tag) => (tag === NO_TEAM_KEY ? NO_TEAM_LABEL : teamName(tag));

// The team rows: every team role (even one that sent nothing), then a team that only the
// campaigns know, then "no team" when a campaign has none. Sorted by click rate, teams whose
// sample is too small and teams that sent nothing last.
export function teamRows(camps, teams, now = new Date()) {
  const byTeam = new Map();
  (teams || []).forEach((t) => byTeam.set(t.tag, { tag: t.tag, lists: t.lists, camps: [] }));
  camps.forEach((c) => campaignTeams(c).forEach((t) => {
    if (!byTeam.has(t)) {
      byTeam.set(t, { tag: t, lists: null, camps: [] });
    }
    byTeam.get(t).camps.push(c);
  }));
  const rows = [...byTeam.values()].map((r) => {
    const a = aggregate(r.camps, now);
    return {
      tag: r.tag, name: teamLabel(r.tag), lists: r.lists, camps: r.camps, a, empty: a.n === 0 && !r.camps.some((c) => c.status !== 'finished'),
    };
  });
  const rank = (r) => {
    if (r.empty) {
      return 2;
    }
    return r.a.enough ? 0 : 1;
  };
  return rows.sort((x, y) => rank(x) - rank(y) || (rank(x) === 0 ? y.a.click - x.a.click : y.a.sent - x.a.sent) || x.name.localeCompare(y.name, 'ko'));
}

// KST pieces of a time (the weekday 0=Mon..6=Sun, the hour), whatever the browser's zone.
const kstParts = (iso) => {
  const p = new Intl.DateTimeFormat('en-US', {
    timeZone: 'Asia/Seoul', weekday: 'short', hour: '2-digit', hourCycle: 'h23',
  }).formatToParts(new Date(iso)).reduce((o, x) => ({ ...o, [x.type]: x.value }), {});
  return { wd: ['Mon', 'Tue', 'Wed', 'Thu', 'Fri', 'Sat', 'Sun'].indexOf(p.weekday), hour: Number(p.hour) };
};
export const WEEKDAYS = ['월', '화', '수', '목', '금', '토', '일'];
export const HOUR_BANDS = ['00–06시', '06–12시', '12–18시', '18–24시'];
export const LENGTH_BANDS = ['20자 이하', '21~40자', '41자 이상'];
export const weekdayOf = (c) => kstParts(c.startedAt).wd;
export const hourBandOf = (c) => Math.floor(kstParts(c.startedAt).hour / 6);
export const lengthBandOf = (c) => {
  const n = [...String(c.subject || '')].length;
  if (n <= 20) {
    return 0;
  }
  return n <= 40 ? 1 : 2;
};

// "11-24(화) 01:05" in KST.
export function formatKST(iso) {
  const d = new Date(iso);
  const p = new Intl.DateTimeFormat('en-US', {
    timeZone: 'Asia/Seoul', month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', hourCycle: 'h23',
  }).formatToParts(d).reduce((o, x) => ({ ...o, [x.type]: x.value }), {});
  return `${p.month}-${p.day}(${WEEKDAYS[weekdayOf({ startedAt: iso })]}) ${p.hour}:${p.minute}`;
}

// Groups of campaigns by a function of the campaign: the label, the campaigns, the totals,
// whether the group may be compared (enough campaigns and messages), and the best one (only
// when two or more groups may be compared).
export function groupBy(camps, labels, indexOf, now = new Date()) {
  const rows = labels.map((label, i) => {
    const cs = camps.filter((c) => classify(c, now).rateOk && indexOf(c) === i);
    const a = aggregate(cs, now);
    return {
      label, n: cs.length, a, ok: a.enough,
    };
  });
  const ok = rows.filter((r) => r.ok);
  const best = ok.length >= 2 ? ok.reduce((b, r) => (r.a.click > b.a.click ? r : b)) : null;
  return { rows, ok, best };
}

// The lists card: a campaign with exactly one list that the caller may read goes to that list
// by name, every other campaign is merged into "other teams' lists" (a campaign that is not
// the caller's carries no list). Campaigns of several lists make a group of their own.
export function listGroups(camps, now = new Date()) {
  const named = new Map();
  const others = [];
  camps.filter((c) => classify(c, now).rateOk).forEach((c) => {
    if (c.own && c.lists && c.lists.length === 1) {
      const k = c.lists[0].name;
      named.set(k, [...(named.get(k) || []), c]);
    } else {
      others.push(c);
    }
  });
  const rows = [...named.entries()].map(([label, cs]) => ({ label, hidden: false, a: aggregate(cs, now) }));
  if (others.length > 0) {
    rows.push({ label: `다른 팀·여러 리스트 캠페인 ${others.length}건`, hidden: true, a: aggregate(others, now) });
  }
  const out = rows.map((r) => ({ ...r, ok: r.a.sRate >= MIN.campaignSent }));
  out.sort((x, y) => y.a.click - x.a.click);
  const ok = out.filter((r) => r.ok && !r.hidden);
  const best = ok.length >= 2 ? ok.reduce((b, r) => (r.a.click > b.a.click ? r : b)) : null;
  return { rows: out, best };
}

// Overlap with other mails of the last 30 days: the campaigns that have send records for
// (nearly) every message, the people who also got another mail, and the sum.
export function overlapRows(camps, now = new Date()) {
  const rows = camps.filter((c) => {
    const k = classify(c, now);
    return k.state === 'done' && k.settled && c.sent > 0 && c.ledgerRows >= c.sent * 0.95;
  }).map((c) => ({
    id: c.id, name: c.name, own: c.own, overlap: c.overlap30, ledger: c.ledgerRows, rate: pct(c.overlap30, c.ledgerRows),
  }));
  rows.sort((x, y) => y.rate - x.rate);
  const ledger = rows.reduce((s, r) => s + r.ledger, 0);
  const overlap = rows.reduce((s, r) => s + r.overlap, 0);
  return {
    rows, ledger, overlap, rate: pct(overlap, ledger),
  };
}

// The groups of recipients the server sent for one team ('' for everyone), as rows with rates
// in the order of `order` (keys), the rest by size; the merged "other" group last.
export function recipientRows(groups, team, order = null) {
  const g = (groups || []).filter((x) => x.team === team).map((x) => ({
    key: x.key, sent: x.sent, click: pct(x.clicked, x.sent), open: pct(x.opened, x.sent), bounce: pct(x.bounced, x.sent), ok: x.sent >= MIN.groupSent,
  }));
  const rank = (r) => {
    if (r.key === '__other__') {
      return 1e9;
    }
    return order ? order.indexOf(r.key) : 0;
  };
  g.sort((x, y) => rank(x) - rank(y) || y.sent - x.sent);
  return g;
}

// Follow-up pairs: the campaigns that are follow-ups of another one, with the original.
export function followupPairs(camps, now = new Date()) {
  const byID = new Map(camps.map((c) => [c.id, c]));
  return camps.filter((c) => c.followupOf && classify(c, now).state === 'done' && classify(c, now).settled)
    .map((c) => ({ followup: c, original: byID.get(c.followupOf) || null }));
}

// One sentence on the top team table for the caller's team (null when the team sent nothing
// that counts).
export function teamSummary(rows, all, prev, myTag) {
  const mine = rows.find((r) => r.tag === myTag);
  if (!mine || mine.a.nRate === 0) {
    return null;
  }
  const { a } = mine;
  const parts = [];
  parts.push(`${mine.name}의 클릭률 ${pctText(a.click)}는 전사 ${pctText(all.click)}보다 ${(a.click - all.click) >= 0 ? '+' : ''}${(a.click - all.click).toFixed(1)}%p`);
  if (prev && prev.nRate > 0) {
    parts[0] += `, 직전 기간보다 ${(a.click - prev.click) >= 0 ? '+' : ''}${(a.click - prev.click).toFixed(1)}%p입니다.`;
  } else {
    parts[0] += '입니다. 직전 기간에는 발송이 없어 시간 비교는 아직 없습니다.';
  }
  parts.push(bounceOver(a.bounce)
    ? `반송률 ${pctText(a.bounce)}는 기준 ${BASELINE.bounce}%를 넘었습니다.`
    : `반송률 ${pctText(a.bounce)}는 기준 ${BASELINE.bounce}% 아래입니다.`);
  return parts.join(' ');
}

// The summary of one campaign for its own screen: what counts, the four rates and the click
// rate against the middle value of the team that sent it (the campaigns the screen read).
export function campaignSummary(c, camps, now = new Date()) {
  const k = classify(c, now);
  const team = campaignTeams(c)[0];
  const peers = camps.filter((x) => campaignTeams(x).includes(team));
  const median = k.rateOk ? teamMedian(peers, now) : null;
  const click = clickRate(c);
  return {
    k,
    team,
    teamName: teamLabel(team),
    click,
    open: pct(c.viewers, c.sent),
    bounce: pct(c.hard + c.soft, c.sent),
    complaint: pct(c.complaint, c.sent, 2),
    median,
    cmp: median ? change(click - median.value, 'click') : null,
    small: c.sent < MIN.campaignSent,
  };
}
