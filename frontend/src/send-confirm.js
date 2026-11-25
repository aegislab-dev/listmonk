// The pre-send confirmation (LST-48): what the window says for an answer of
// GET /api/campaigns/:id/preflight. Everything here returns i18n keys with their params;
// the component translates. The functions do not read the clock or the browser unless
// they are asked to, so they can be tested.
// The extension is for the node tests, which do not resolve without it.
// eslint-disable-next-line import/extensions
import { trackingConfirmLines } from './tracking-check.js';

const K = 'campaigns.sendConfirm';

const RANK = {
  block: 0, warn: 1, ok: 2, info: 3,
};

const pad = (n) => String(n).padStart(2, '0');

// "UTC", "UTC+9", "UTC+5:30", "UTC-5" for an offset in minutes.
export const offsetLabel = (minutes) => {
  if (minutes === 0) return 'UTC';
  const abs = Math.abs(minutes);
  const h = Math.floor(abs / 60);
  const m = abs % 60;
  return `UTC${minutes < 0 ? '-' : '+'}${h}${m ? `:${pad(m)}` : ''}`;
};

// A moment as the wall clock of a time zone shows it. tz: an IANA name, or undefined for
// the one of this browser.
export const zonedParts = (date, tz, lang = 'ko') => {
  const dtf = new Intl.DateTimeFormat('en-US', {
    timeZone: tz, hourCycle: 'h23', year: 'numeric', month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit',
  });
  const p = {};
  dtf.formatToParts(date).forEach(({ type, value }) => { p[type] = value; });
  const weekday = new Intl.DateTimeFormat(lang, { timeZone: tz, weekday: 'short' }).format(date);
  // The offset is what the wall clock reads minus the real moment (seconds dropped on both sides).
  const wall = Date.UTC(+p.year, +p.month - 1, +p.day, +p.hour, +p.minute);
  const offset = Math.round((wall - Math.floor(date.getTime() / 60000) * 60000) / 60000);
  return {
    year: p.year, month: p.month, day: p.day, hour: p.hour, minute: p.minute, weekday, offset,
  };
};

// "2026-11-20 (금) 09:00" with the zone name and its offset ("Asia/Seoul", "UTC+9").
export const scheduleDisplay = (iso, { tz, lang = 'ko' } = {}) => {
  const date = new Date(iso);
  const z = zonedParts(date, tz, lang);
  const name = tz || Intl.DateTimeFormat().resolvedOptions().timeZone || 'UTC';
  return {
    text: `${z.year}-${z.month}-${z.day} (${z.weekday}) ${z.hour}:${z.minute}`,
    month: String(+z.month),
    day: String(+z.day),
    time: `${z.hour}:${z.minute}`,
    zone: name,
    offset: offsetLabel(z.offset),
    offsetMinutes: z.offset,
  };
};

// The time left to a moment, as days/hours/minutes. null when it is not ahead.
export const timeUntil = (iso, now) => {
  const ms = new Date(iso).getTime() - now.getTime();
  if (!(ms > 0)) return null;
  const total = Math.floor(ms / 60000);
  return { days: Math.floor(total / 1440), hours: Math.floor((total % 1440) / 60), minutes: total % 60 };
};

// The key for "in 1 day 3 hours" style text, by the largest unit that matters.
export const untilKey = (t) => {
  if (t.days > 0) return { key: `${K}.until.days`, params: t };
  if (t.hours > 0) return { key: `${K}.until.hours`, params: t };
  return { key: `${K}.until.minutes`, params: t };
};

// A text with **bold** and `code` marks as parts: {text, bold, code}.
export const richParts = (text) => text.split(/(\*\*.+?\*\*|`.+?`)/).filter((t) => t !== '').map((t) => {
  if (t.length > 4 && t.startsWith('**') && t.endsWith('**')) return { text: t.slice(2, -2), bold: true };
  if (t.length > 2 && t.startsWith('`') && t.endsWith('`')) return { text: t.slice(1, -1), code: true };
  return { text: t };
});

const checkLine = (c, pf) => {
  const p = { ...(c.params || {}) };
  let key = `${K}.check.${c.key}.${c.level}`;
  if (c.key === 'recipients.zero' && c.level === 'warn') {
    key = `${K}.check.recipients.zero.${pf.mode === 'resume' ? 'resume' : 'optin'}`;
  }
  if (c.key === 'recent.notApplied' && p.excluded === undefined) key = `${K}.check.recent.notApplied.noCount`;
  if (c.key === 'unsubscribe.missing') p.tag = '{{ UnsubscribeURL }}';
  return {
    key, params: p, level: c.level, name: c.key,
  };
};

// The lines under "checks before sending", the blocking ones first, then the warnings and
// the passed ones; within a level the order of the server and then of the tracking check.
export const checkLines = (pf) => {
  const lines = (pf.checks || []).map((c) => checkLine(c, pf));

  // Tracking (LST-44): its own report decides, with its own texts. A line that says it is
  // tracked only appears for what is tracked: a plain text mail records no views, a link
  // that is not there records no clicks.
  if (pf.tracking) {
    const t = pf.tracking;
    const warn = trackingConfirmLines(t);
    warn.forEach((l) => lines.push({
      key: l.key, params: l.params || {}, level: 'warn', name: 'tracking', raw: true,
    }));
    const line = (level, key) => lines.push({
      key: `${K}.check.tracking.${key}`, params: {}, level, name: 'tracking',
    });
    if (t.state !== 'off') {
      const viewOn = t.view === 'manual' || t.view === 'auto';
      if (t.view === 'unsupported') line('info', 'viewUnsupported');
      else if (t.view === 'n/a') line('info', 'notApplicable');
      if (!warn.length) {
        if (viewOn && t.links.total > 0) line('ok', 'ok');
        else if (viewOn) line('ok', 'viewOnly');
        else if (t.view === 'unsupported' && t.links.total > 0 && t.links.untracked === 0) line('ok', 'clickOnly');
      }
    }
  } else {
    lines.push({
      key: `${K}.check.tracking.failed`, params: { error: pf.trackingError || '' }, level: 'warn', name: 'tracking',
    });
  }

  return lines
    .map((l, i) => ({ l, i }))
    .sort((a, b) => (RANK[a.l.level] - RANK[b.l.level]) || (a.i - b.i))
    .map(({ l }) => l);
};

// The whole window for an answer: title, button, whether it can be confirmed, the lines.
export const sendConfirmModel = (pf, { tz, lang = 'ko', now = new Date() } = {}) => {
  const resume = pf.mode === 'resume';
  const schedule = pf.mode === 'schedule';
  // LST-67: a campaign that is already scheduled, read again after it was saved. Nothing is asked
  // for: the window says what the send will be and the person keeps or undoes the schedule.
  const scheduled = pf.mode === 'scheduled';
  // A scheduled campaign that was sent from before (paused, then scheduled again) goes on after
  // its checkpoint: its people are the ones left, as for a resume (LST-67 review).
  const left = resume || (scheduled && !!pf.resumed);
  const n = left ? pf.remaining : pf.recipients;
  const all = checkLines(pf);
  const blocked = all.some((c) => c.level === 'block');
  // Nobody to send to (approved design A5): the reason is the only line. Otherwise (a From the
  // send refuses) every line stays, the blocking one first: the rest is to be fixed too (A2).
  const nobody = all.some((c) => c.level === 'block' && (c.name === 'recipients.zero' || c.name === 'lists.none'));
  const checks = nobody ? all.filter((c) => c.level === 'block') : all;
  const warnings = all.filter((c) => c.level === 'warn').length;
  // A paused campaign with a send time is scheduled by the status change, not sent at once.
  const timed = (schedule || scheduled || resume) && pf.sendAt;
  const when = timed ? scheduleDisplay(pf.sendAt, { tz, lang }) : null;
  const until = timed ? timeUntil(pf.sendAt, now) : null;

  let title;
  if (blocked) {
    let why = 'zero';
    if (checks.some((c) => c.name === 'lists.none')) why = 'noList';
    else if (checks.some((c) => c.name === 'from.refused')) why = 'fromRefused';
    title = { key: `${K}.title.${why}`, params: {} };
  } else if (resume && when && until) {
    title = {
      key: `${K}.title.resumeAt`,
      params: {
        n, month: when.month, day: when.day, time: when.time,
      },
    };
  } else if (resume) {
    title = { key: `${K}.title.resume`, params: { n } };
  } else if ((schedule || scheduled) && when) {
    title = {
      key: `${K}.title.${scheduled ? 'scheduled' : 'schedule'}`,
      params: {
        n, month: when.month, day: when.day, time: when.time,
      },
    };
  } else {
    title = { key: `${K}.title.start`, params: { n } };
  }

  let sub;
  if (blocked) sub = { key: `${K}.sub.blocked`, params: {} };
  else if (warnings) sub = { key: `${K}.sub.warn`, params: { n: warnings } };
  else if (resume && when && until) sub = { key: `${K}.sub.resumeAt`, params: {} };
  else sub = { key: `${K}.sub.${pf.mode}`, params: {} };
  if (scheduled && warnings && !blocked) sub = { key: `${K}.sub.scheduledWarn`, params: { n: warnings } };

  // The window of a scheduled campaign has one way on: it stays scheduled (the other button
  // takes the schedule off). No "anyway": nothing is being started here.
  const confirmKey = scheduled
    ? `${K}.review.keep`
    : `${K}.${warnings && !blocked ? 'confirmAnyway' : 'confirm'}.${pf.mode}`;
  const targetStatus = schedule ? 'scheduled' : 'running';
  const asOf = pf.asOf ? new Date(pf.asOf) : null;

  return {
    mode: pf.mode,
    targetStatus: scheduled ? null : targetStatus,
    count: n,
    title,
    sub,
    confirm: { key: confirmKey, params: { n } },
    // A scheduled campaign is already saved: the window has nothing to disable.
    disabled: blocked && !scheduled,
    blocked,
    warnings,
    checks,
    when,
    until,
    // The browser's clock differs from Korea: say what the time is there (LST-50 fixes the
    // input to Korean time; then this stays empty).
    other: when && when.offsetMinutes !== 540
      ? scheduleDisplay(pf.sendAt, { tz: 'Asia/Seoul', lang })
      : null,
    overlap: pf.overlap || 0,
    excluded: pf.excluded || 0,
    zero: !resume && n === 0,
    asOf,
  };
};
