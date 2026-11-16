// Drawing N people from a list into a new list: input checks, the request body and the
// default name. Plain data only; the server decides who is drawn.
export const DRAW_TAG = '뽑은-리스트';
export const DRAW_MAX_COUNT = 10000000;
export const DRAW_MAX_DAYS = 3650;
export const LIST_NAME_MAX = 200;

const WHOLE = /^\d+$/;

// '' when the form is valid, else the i18n key suffix of the problem (lists.draw.<key>).
export function validateDraw(form) {
  const value = String(form.value ?? '').trim();
  if (!WHOLE.test(value)) return 'valueRequired';
  const n = Number(value);
  if (form.unit === 'percent' ? (n < 1 || n > 100) : (n < 1 || n > DRAW_MAX_COUNT)) return 'valueRequired';
  if (!['same_source', 'all'].includes(form.scope)) return 'invalid';
  if (form.period === 'days') {
    const days = String(form.days ?? '').trim();
    if (!WHOLE.test(days) || Number(days) < 1 || Number(days) > DRAW_MAX_DAYS) return 'daysRequired';
  }
  return '';
}

// The body of the preview and draw requests; the draw also takes the list name.
export function drawBody(form) {
  return {
    unit: form.unit === 'percent' ? 'percent' : 'count',
    value: Number(String(form.value).trim()),
    exclude_scope: form.scope,
    exclude_days: form.period === 'days' ? Number(String(form.days).trim()) : 0,
  };
}

// "{source} · {suffix}" with the source name cut so the whole name fits the list name limit.
export function defaultDrawName(source, suffix) {
  const tail = ` · ${suffix}`;
  const room = LIST_NAME_MAX - [...tail].length;
  const head = [...source];
  return (head.length > room ? head.slice(0, room).join('') : source) + tail;
}

// YYYY-MM-DD of a date in the browser's time zone.
export function localDate(d = new Date()) {
  const pad = (n) => String(n).padStart(2, '0');
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}`;
}

// How many a request draws out of its candidates (the server clamps the same way).
export function willDraw(counts) {
  return counts ? Math.min(counts.requested, counts.candidates) : 0;
}

// What the draw record's settings say, for display. The API client turns the stored keys
// into camelCase, so both spellings are read.
export function recordSettings(settings) {
  const s = settings || {};
  return {
    unit: s.unit === 'percent' ? 'percent' : 'count',
    value: Number(s.value) || 0,
    scope: (s.exclude_scope ?? s.excludeScope) === 'all' ? 'all' : 'same_source',
    days: Number(s.exclude_days ?? s.excludeDays) || 0,
  };
}
