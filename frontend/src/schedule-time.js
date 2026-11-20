// Scheduled send time in a chosen time zone (LST-50).
//
// The campaign stores one moment (`send_at`, an ISO instant). The picker on the campaign screen is a
// browser date picker that works on the browser's *local* fields, so this module converts between the
// moment and a "wall" Date whose local fields read as the time in the chosen zone (default KST).
// The chosen zone is kept in the campaign's `attribs` under `axm_send_tz` (an IANA name, no migration).
//
// Plain functions, no browser or Vue beyond Intl/localStorage, so tests/schedule-time.test.mjs runs
// them with node under several TZ values.

export const KST = 'Asia/Seoul';
export const TZ_ATTRIB = 'axm_send_tz';

// The zones shown at the top of the picker and in the line under the input (the chosen zone is left out).
export const FREQUENT_ZONES = [KST, 'America/New_York', 'America/Los_Angeles', 'Asia/Tokyo', 'Europe/London'];

const RECENT_KEY = 'axm.sendTz.recent';
const RECENT_MAX = 5;

// Korean names of the cities people look for, to search and show beside the generic zone name.
export const KO_CITIES = {
  'Asia/Seoul': '서울',
  'Asia/Tokyo': '도쿄',
  'Asia/Shanghai': '상하이',
  'Asia/Hong_Kong': '홍콩',
  'Asia/Taipei': '타이베이',
  'Asia/Singapore': '싱가포르',
  'Asia/Bangkok': '방콕',
  'Asia/Ho_Chi_Minh': '호찌민',
  'Asia/Jakarta': '자카르타',
  'Asia/Manila': '마닐라',
  'Asia/Kuala_Lumpur': '쿠알라룸푸르',
  'Asia/Kolkata': '콜카타',
  'Asia/Dubai': '두바이',
  'Asia/Tehran': '테헤란',
  'Asia/Karachi': '카라치',
  'Asia/Dhaka': '다카',
  'Asia/Kathmandu': '카트만두',
  'Asia/Riyadh': '리야드',
  'Asia/Jerusalem': '예루살렘',
  'Asia/Istanbul': '이스탄불',
  'Europe/London': '런던',
  'Europe/Dublin': '더블린',
  'Europe/Paris': '파리',
  'Europe/Berlin': '베를린',
  'Europe/Madrid': '마드리드',
  'Europe/Rome': '로마',
  'Europe/Amsterdam': '암스테르담',
  'Europe/Zurich': '취리히',
  'Europe/Stockholm': '스톡홀름',
  'Europe/Warsaw': '바르샤바',
  'Europe/Athens': '아테네',
  'Europe/Istanbul': '이스탄불',
  'Europe/Moscow': '모스크바',
  'Europe/Kyiv': '키이우',
  'Africa/Cairo': '카이로',
  'Africa/Lagos': '라고스',
  'Africa/Nairobi': '나이로비',
  'Africa/Johannesburg': '요하네스버그',
  'America/New_York': '뉴욕',
  'America/Toronto': '토론토',
  'America/Chicago': '시카고',
  'America/Denver': '덴버',
  'America/Phoenix': '피닉스',
  'America/Los_Angeles': 'LA',
  'America/Vancouver': '밴쿠버',
  'America/Anchorage': '앵커리지',
  'Pacific/Honolulu': '호놀룰루',
  'America/Mexico_City': '멕시코시티',
  'America/Bogota': '보고타',
  'America/Lima': '리마',
  'America/Santiago': '산티아고',
  'America/Sao_Paulo': '상파울루',
  'America/Argentina/Buenos_Aires': '부에노스아이레스',
  'America/St_Johns': '뉴펀들랜드',
  'Australia/Sydney': '시드니',
  'Australia/Melbourne': '멜버른',
  'Australia/Perth': '퍼스',
  'Pacific/Auckland': '오클랜드',
  UTC: '협정 세계시',
};

// Intl's short name for these is "GMT+9"; people say KST and JST.
const FIXED_ABBR = { 'Asia/Seoul': 'KST', 'Asia/Tokyo': 'JST' };

const MIN = 60000;
const DAY = 86400000;

const fmtCache = new Map();
function wallFormat(tz) {
  let f = fmtCache.get(tz);
  if (!f) {
    f = new Intl.DateTimeFormat('en-US', {
      timeZone: tz,
      hourCycle: 'h23',
      year: 'numeric',
      month: 'numeric',
      day: 'numeric',
      hour: 'numeric',
      minute: 'numeric',
      second: 'numeric',
    });
    fmtCache.set(tz, f);
  }
  return f;
}

const nameFmtCache = new Map();
function nameFormat(locale, style, tz) {
  const key = `${locale}|${style}|${tz}`;
  let f = nameFmtCache.get(key);
  if (!f) {
    f = new Intl.DateTimeFormat(locale, { timeZone: tz, timeZoneName: style });
    nameFmtCache.set(key, f);
  }
  return f;
}

// True when `tz` is a time zone name this browser knows.
export function validZone(tz) {
  if (typeof tz !== 'string' || !tz.trim()) return false;
  try {
    // eslint-disable-next-line no-new
    new Intl.DateTimeFormat('en', { timeZone: tz });
    return true;
  } catch (e) {
    return false;
  }
}

// Milliseconds to add to an instant to get the wall clock of `tz` (as if it were UTC).
export function zoneOffsetMs(tz, instantMs) {
  const sec = Math.floor(instantMs / 1000) * 1000;
  const p = {};
  wallFormat(tz).formatToParts(new Date(sec)).forEach((x) => { p[x.type] = parseInt(x.value, 10); });
  return Date.UTC(p.year, p.month - 1, p.day, p.hour, p.minute, p.second) - sec;
}

// The moment `date` as a Date whose *local* fields are the clock time in `tz` (for the picker).
export function instantToWall(date, tz) {
  const w = new Date(date.getTime() + zoneOffsetMs(tz, date.getTime()));
  return new Date(w.getUTCFullYear(), w.getUTCMonth(), w.getUTCDate(), w.getUTCHours(), w.getUTCMinutes());
}

// The picker's Date, whose local fields are a clock time in `tz`, as the real moment.
// A clock time that does not exist (the hour skipped when clocks go forward) moves one hour later;
// one that happens twice (clocks go back) is the first of the two.
export function wallToInstant(wall, tz) {
  const guess = Date.UTC(wall.getFullYear(), wall.getMonth(), wall.getDate(), wall.getHours(), wall.getMinutes());
  const offsets = [...new Set([zoneOffsetMs(tz, guess - DAY), zoneOffsetMs(tz, guess + DAY)])];
  const valid = offsets.map((o) => guess - o).filter((t) => zoneOffsetMs(tz, t) === guess - t);
  if (valid.length) return new Date(Math.min(...valid));
  // In the gap: use the offset from before the change.
  return new Date(guess - zoneOffsetMs(tz, guess - DAY));
}

// { ymd: '2026-11-21', md: '11/21', wd: 0-6 (0 = Sunday), hm: '09:00' } for the moment in `tz`.
export function zoneParts(date, tz) {
  const w = new Date(date.getTime() + zoneOffsetMs(tz, date.getTime()));
  const pad = (n) => String(n).padStart(2, '0');
  const month = pad(w.getUTCMonth() + 1);
  const day = pad(w.getUTCDate());
  return {
    ymd: `${w.getUTCFullYear()}-${month}-${day}`,
    md: `${month}/${day}`,
    wd: w.getUTCDay(),
    hm: `${pad(w.getUTCHours())}:${pad(w.getUTCMinutes())}`,
  };
}

// "UTC+9", "UTC−5", "UTC+5:30" for `tz` at the moment.
export function offsetLabel(date, tz) {
  const min = Math.round(zoneOffsetMs(tz, date.getTime()) / MIN);
  const sign = min < 0 ? '−' : '+';
  const abs = Math.abs(min);
  const h = Math.floor(abs / 60);
  const m = abs % 60;
  return `UTC${sign}${h}${m ? `:${String(m).padStart(2, '0')}` : ''}`;
}

// "KST", "EST", "PST", "UTC+13": a short name that is the same on every browser language.
export function zoneAbbr(date, tz) {
  if (FIXED_ABBR[tz]) return FIXED_ABBR[tz];
  let name = '';
  try {
    name = nameFormat('en-US', 'short', tz)
      .formatToParts(date).find((p) => p.type === 'timeZoneName')?.value || '';
  } catch (e) {
    name = '';
  }
  // "GMT+1", "GMT-3:30" are not names: show the offset the same way everywhere.
  if (!name || /^GMT[+\-−]/.test(name)) return offsetLabel(date, tz);
  return name;
}

// The generic Korean name ("미 동부 시간") when the browser has one.
export function zoneKoName(date, tz) {
  try {
    const name = nameFormat('ko-KR', 'longGeneric', tz)
      .formatToParts(date).find((p) => p.type === 'timeZoneName')?.value || '';
    return /^GMT/.test(name) ? '' : name;
  } catch (e) {
    return '';
  }
}

// The city to show: the Korean name from the table, else the last part of the IANA name.
export function zoneCity(tz) {
  if (KO_CITIES[tz]) return KO_CITIES[tz];
  const last = tz.split('/').pop();
  return last.replace(/_/g, ' ');
}

// { tz, abbr, name, city, offset, parts } for a row of the picker, at the moment `date`.
export function zoneInfo(tz, date) {
  const city = zoneCity(tz);
  const name = zoneKoName(date, tz);
  return {
    tz,
    abbr: zoneAbbr(date, tz),
    name: name || city,
    city: name && city && !name.includes(city) ? city : '',
    offset: offsetLabel(date, tz),
    parts: zoneParts(date, tz),
  };
}

// Every zone the browser knows (plus the frequent ones, if an old browser lists none).
export function allZones() {
  let list = [];
  try {
    list = typeof Intl.supportedValuesOf === 'function' ? Intl.supportedValuesOf('timeZone') : [];
  } catch (e) {
    list = [];
  }
  const set = new Set([...FREQUENT_ZONES, 'UTC', ...list]);
  return [...set].filter(validZone);
}

// A zone typed by hand ("europe/paris"): its proper name when this browser knows it, else ''.
// Lets a person pick a zone even where the browser cannot list zones.
export function customZone(text) {
  const t = String(text || '').trim();
  if (!/^(UTC|[A-Za-z][A-Za-z0-9_+-]*(\/[A-Za-z0-9_+-]+)+)$/.test(t) || !validZone(t)) return '';
  // The listed spelling when the browser lists it ("europe/paris" -> "Europe/Paris").
  const listed = allZones().find((z) => z.toLowerCase() === t.toLowerCase());
  if (listed) return listed;
  try {
    return new Intl.DateTimeFormat('en', { timeZone: t }).resolvedOptions().timeZone;
  } catch (e) {
    return '';
  }
}

const norm = (s) => String(s || '')
  .toLowerCase()
  .replace(/[_/-]+/g, ' ')
  .replace(/\s+/g, ' ')
  .trim();

// One entry per zone with what a search looks at. Built once when the picker opens.
export function buildIndex(zones, date) {
  return zones.map((tz) => {
    const info = zoneInfo(tz, date);
    return { ...info, fields: [norm(tz), norm(info.city), norm(info.name), norm(info.abbr), norm(KO_CITIES[tz])] };
  });
}

// The entries that match a search text (Korean name, city, IANA name, abbreviation), best first.
export function searchIndex(query, index) {
  const q = norm(query);
  if (!q) return [];
  const hits = [];
  index.forEach((e) => {
    let rank = -1;
    if (e.fields.some((f) => f === q)) rank = 0;
    else if (e.fields.some((f) => f.startsWith(q))) rank = 1;
    else if (e.fields.some((f) => f.split(' ').some((w) => w.startsWith(q)))) rank = 2;
    else if (e.fields.some((f) => f.includes(q))) rank = 3;
    if (rank >= 0) hits.push({ rank, e });
  });
  return hits.sort((a, b) => a.rank - b.rank || a.e.tz.localeCompare(b.e.tz)).map((h) => h.e);
}

// The line under the input: KST first (always, unless KST is the chosen zone), then the frequent
// zones that are not the chosen one. [{ tz, abbr, city, parts }]
export function otherZoneLines(date, chosen) {
  return FREQUENT_ZONES.filter((tz) => tz !== chosen).map((tz) => ({
    tz,
    kst: tz === KST,
    city: zoneCity(tz),
    parts: zoneParts(date, tz),
  }));
}

// The frontend camelCases the keys of the attribs it gets from the API (axm_send_tz -> axmSendTz),
// and the screen sends them back as they are, so the key can come in either form.
const TZ_ATTRIB_CAMEL = 'axmSendTz';

// The campaign's zone from its attribs (KST when none, or one this browser does not know), whether
// one is stored, and the attribs without it so the JSON tab does not show it.
export function splitAttribs(attribs) {
  const rest = { ...(attribs || {}) };
  const raw = rest[TZ_ATTRIB] ?? rest[TZ_ATTRIB_CAMEL];
  delete rest[TZ_ATTRIB];
  delete rest[TZ_ATTRIB_CAMEL];
  const stored = validZone(raw);
  return { tz: stored ? raw : KST, stored, rest };
}

// The attribs to save: the zone is stored only for a scheduled campaign.
export function mergeAttribs(attribs, tz, scheduled) {
  const out = { ...(attribs || {}) };
  delete out[TZ_ATTRIB];
  delete out[TZ_ATTRIB_CAMEL];
  if (scheduled && validZone(tz)) out[TZ_ATTRIB] = tz;
  return out;
}

// True when this computer's own clock offset differs from `tz` at that moment.
export function browserDiffers(date, tz) {
  return -date.getTimezoneOffset() * MIN !== zoneOffsetMs(tz, date.getTime());
}

export function browserZoneName() {
  try {
    return Intl.DateTimeFormat().resolvedOptions().timeZone || '';
  } catch (e) {
    return '';
  }
}

// Recently chosen zones, newest first. localStorage may be missing or throw: then there are none.
export function recentZones() {
  try {
    const v = JSON.parse(localStorage.getItem(RECENT_KEY) || '[]');
    return Array.isArray(v) ? v.filter(validZone).slice(0, RECENT_MAX) : [];
  } catch (e) {
    return [];
  }
}

export function rememberZone(tz) {
  if (!validZone(tz)) return;
  try {
    const next = [tz, ...recentZones().filter((z) => z !== tz)].slice(0, RECENT_MAX);
    localStorage.setItem(RECENT_KEY, JSON.stringify(next));
  } catch (e) {
    // Not stored: the picker just has no recent zones next time.
  }
}
