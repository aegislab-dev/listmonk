// Statistics dashboard calculations (LST-64). Plain functions over the answer of
// /api/stats/campaigns: rates, what counts in a rate, teams, comparisons and the groups of
// the "for the next send" cards. No Vue, no browser state: tests/stats.test.mjs pins them.
import {
  MIN, SETTLE_DAYS, BASELINE, NEUTRAL,
} from './stats-definitions.js'; // eslint-disable-line import/extensions
import { trackingStateFor } from './proof-summary.js'; // eslint-disable-line import/extensions
import { teamName } from './teams.js'; // eslint-disable-line import/extensions

export const NO_TEAM_KEY = '__none__';
const DAY_MS = 24 * 60 * 60 * 1000;

// Keep full precision for comparisons and round only when displaying a rate.
export const pct = (n, d) => (d > 0 ? (n / d) * 100 : null);
export const fmt = (n) => Number(n).toLocaleString('en-US');
export const pctText = (v, digits = 1) => (v === null ? '—' : `${v.toFixed(digits)}%`);
export const statsNow = (now = new Date()) => new Date(Math.floor(now.getTime() / 300000) * 300000 + 300000);

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
  const tracked = trackingStateFor({
    privacy: { disable_tracking: !!c.trackingOff, individual_tracking: true },
    sent: c.sent,
    viewsTotal: c.viewers,
    clicksTotal: c.clickers,
  });
  return {
    state,
    settled,
    hours: Math.max(0, Math.floor(age / 3600000)),
    collecting: done && !settled,
    untracked: c.sent > 0 && !tracked,
    rateOk: done && tracked && settled && c.sent > 0,
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
  a.complaint = pct(a.complaints, a.sBounce);
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
  return v.length % 2 ? v[m] : (v[m - 1] + v[m]) / 2;
}
export function teamMedian(camps, now = new Date()) {
  const rates = camps.filter((c) => c.sent >= MIN.campaignSent && classify(c, now).rateOk).map(clickRate);
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
export function previousStatus(previous, failed) {
  if (failed) return 'previousError';
  if (!previous?.n) return 'noPrev';
  return previous.nRate ? 'previousSmall' : 'previousUnavailable';
}

export const signedPoints = (delta) => `${delta > 0 ? '+' : ''}${delta.toFixed(1)}%p`;

export const changeText = (ch, t) => {
  const v = signedPoints(ch.delta);
  if (ch.dir === 'same') {
    return t('stats.similar', { delta: v });
  }
  return `${ch.dir === 'up' ? '▲' : '▼'} ${v}`;
};

// Compare counts with policy thresholds before applying display rounding.
export const bounceOver = (count, sent, min = MIN.campaignSent) => (
  sent >= min && count * 100 > BASELINE.bounce * sent
);
export const complaintOver = (count, sent, min = MIN.complaintSent) => (
  sent >= min && count * 1000 > (BASELINE.complaint * 10) * sent
);

// The teams of a campaign as tags; a campaign without a team is NO_TEAM_KEY.
export const campaignTeams = (c) => (c.teamTags && c.teamTags.length > 0 ? c.teamTags : [NO_TEAM_KEY]);
export const teamLabel = (tag, t) => (tag === NO_TEAM_KEY ? t('stats.noTeam') : teamName(tag));

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
      tag: r.tag, lists: r.lists, camps: r.camps, a, empty: a.n === 0 && !r.camps.some((c) => c.status !== 'finished'),
    };
  });
  const rank = (r) => {
    if (r.empty) {
      return 2;
    }
    return r.a.enough ? 0 : 1;
  };
  return rows.sort((x, y) => rank(x) - rank(y) || (rank(x) === 0 ? y.a.click - x.a.click : y.a.sent - x.a.sent) || x.tag.localeCompare(y.tag));
}

// KST pieces of a time (the weekday 0=Mon..6=Sun, the hour), whatever the browser's zone.
const kstParts = (iso) => {
  const p = new Intl.DateTimeFormat('en-US', {
    timeZone: 'Asia/Seoul', weekday: 'short', hour: '2-digit', hourCycle: 'h23',
  }).formatToParts(new Date(iso)).reduce((o, x) => ({ ...o, [x.type]: x.value }), {});
  return { wd: ['Mon', 'Tue', 'Wed', 'Thu', 'Fri', 'Sat', 'Sun'].indexOf(p.weekday), hour: Number(p.hour) };
};
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
export function formatKST(iso, locale = 'en') {
  const d = new Date(iso);
  const p = new Intl.DateTimeFormat('en-US', {
    timeZone: 'Asia/Seoul', month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', hourCycle: 'h23',
  }).formatToParts(d).reduce((o, x) => ({ ...o, [x.type]: x.value }), {});
  const weekday = new Intl.DateTimeFormat(locale, { timeZone: 'Asia/Seoul', weekday: 'short' }).format(d);
  return `${p.month}-${p.day}(${weekday}) ${p.hour}:${p.minute}`;
}

// Groups of campaigns by a function of the campaign: the label, the campaigns, the totals,
// whether the group may be compared (enough campaigns and messages), and the best one (only
// when two or more groups may be compared).
export function groupBy(camps, labels, indexOf, now = new Date()) {
  const rows = labels.map((label, i) => {
    const cs = camps.filter((c) => classify(c, now).bounceOk && indexOf(c) === i);
    const a = aggregate(cs, now);
    return {
      label, n: cs.length, a, ok: a.enough,
    };
  });
  const ok = rows.filter((r) => r.ok);
  const best = ok.length >= 2 ? ok.reduce((b, r) => (r.a.click > b.a.click ? r : b)) : null;
  return { rows, ok, best };
}

// Named sources may cover multiple lists. Untagged lists keep their ID as their identity.
export function listGroups(camps, now = new Date()) {
  const groups = new Map();
  camps.filter((c) => classify(c, now).bounceOk).forEach((c) => {
    const list = c.own && c.listsN === 1 ? c.lists?.[0] : null;
    const hidden = !c.own || (c.listsN === 1 && !list);
    let key = hidden ? 'hidden' : 'multiple';
    if (list) key = list.source ? `source:${list.source}` : `list:${list.id}`;
    if (!groups.has(key)) {
      groups.set(key, {
        key, label: list?.source || list?.name, list, hidden, camps: [],
      });
    }
    groups.get(key).camps.push(c);
  });
  const rows = [...groups.values()].map(({ camps: cs, ...g }) => {
    const a = aggregate(cs, now);
    return { ...g, a, ok: a.sRate >= MIN.campaignSent };
  });
  rows.sort((x, y) => (y.a.click ?? -1) - (x.a.click ?? -1));
  const ok = rows.filter((r) => r.ok && !r.hidden);
  const best = ok.length >= 2 ? ok.reduce((b, r) => (r.a.click > b.a.click ? r : b)) : null;
  return { rows, best };
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
    key: x.key,
    sent: x.sent,
    recipients: x.recipients,
    click: pct(x.clicked, x.sent),
    open: pct(x.opened, x.sent),
    bounce: pct(x.bounced, x.sent),
    ok: x.recipients >= MIN.recipientSample && x.key !== '__other__',
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

// Include the pair even when the original started before the selected period.
export function followupPairs(camps, now = new Date()) {
  return camps.filter((c) => {
    const k = classify(c, now);
    return c.followupOf && k.state === 'done' && k.settled;
  }).map((followup) => {
    const tracked = !followup.trackingOff && (followup.combinedClickers > 0 || followup.combinedViewers > 0);
    return {
      followup,
      click: tracked ? pct(followup.combinedClickers, followup.followupParentSent) : null,
      open: tracked ? pct(followup.combinedViewers, followup.followupParentSent) : null,
    };
  });
}

export function campaignSummary(c, camps, now = new Date()) {
  const k = classify(c, now);
  const team = campaignTeams(c)[0];
  const peers = camps.filter((x) => x.id !== c.id);
  const small = c.sent < MIN.campaignSent;
  const teamValue = k.rateOk && !small ? teamMedian(peers.filter((x) => campaignTeams(x).includes(team)), now) : null;
  const allValue = k.rateOk && !small && !teamValue ? teamMedian(peers, now) : null;
  const median = teamValue || allValue;
  const click = clickRate(c);
  return {
    k,
    team: allValue ? '' : team,
    click,
    open: pct(c.viewers, c.sent),
    bounce: pct(c.hard + c.soft, c.sent),
    complaint: pct(c.complaint, c.sent),
    bounceWarning: k.bounceOk && bounceOver(c.hard + c.soft, c.sent),
    complaintWarning: k.bounceOk && complaintOver(c.complaint, c.sent),
    median,
    cmp: median ? change(click - median.value, 'click') : null,
    small,
  };
}
