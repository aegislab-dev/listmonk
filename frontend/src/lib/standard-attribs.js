// The standard attribute dictionary on the browser side. The entries themselves come from
// the server (/api/config, axm_standard_attribs: [{ key, kind, aliases }]); this module only
// knows what to do with them: find the standard item a column name means, and bring a value
// into the item's format. It has no state and runs in the page, the import Worker and Node.
// The extension lets the Node test runner load this module without a bundler.
import { COUNTRIES, COUNTRY_ALIASES } from './countries.js'; // eslint-disable-line import/extensions

// Folds a column name or key for alias comparison (same rule as NormalizeAttribName in Go).
export const normalizeName = (name) => String(name ?? '').trim().toLowerCase().replace(/[\s_-]+/g, '');

// The dictionary entry a column name or a typed key means, or null. An entry answers to its
// own key and to its aliases.
export function standardFor(dictionary, name) {
  const wanted = normalizeName(name);
  if (!wanted) return null;
  return (dictionary || []).find((entry) => [entry.key, ...(entry.aliases || [])]
    .some((alias) => normalizeName(alias) === wanted)) || null;
}

// The dictionary entry with exactly this key, or null.
export const standardByKey = (dictionary, key) => (dictionary || []).find((entry) => entry.key === key) || null;

// Puts the standard keys first, in dictionary order, and drops repeats.
export const withStandardFirst = (dictionary, keys) => [...new Set([...(dictionary || []).map(({ key }) => key), ...keys])];

// Folds a value for name lookup: no accents, letter case or punctuation.
const fold = (text) => String(text).normalize('NFKD').replace(/\p{M}+/gu, '').toLowerCase()
  .replace(/[^\p{L}\p{N}]+/gu, ' ')
  .trim();

// Cell texts that mean "not known". A standard item is left out rather than given such a value.
const BLANK_WORDS = new Set(['', 'n a', 'none', 'null', 'unknown', 'undefined', 'nan', 'tbd',
  '없음', '모름', '미상', '해당없음', '해당 없음', '미정'].map(fold));
const isBlank = (text) => BLANK_WORDS.has(fold(text));

const CODES = new Set(COUNTRIES.map(([code]) => code));
let countryNames;
function countryByName(text) {
  if (!countryNames) {
    countryNames = new Map();
    COUNTRIES.forEach(([code, ...names]) => names.forEach((name) => countryNames.set(fold(name), code)));
    Object.entries(COUNTRY_ALIASES).forEach(([code, names]) => names.forEach((name) => countryNames.set(fold(name), code)));
  }
  return countryNames.get(fold(text)) || '';
}

// The ISO 3166-1 alpha-2 code a text names, or ''. A two-letter code in any letter case, a
// country name in English or Korean, a common abbreviation, or a name with its local name in
// brackets ("Poland (Polska)") when both parts are the same country.
export function countryCode(text) {
  const t = String(text ?? '').trim();
  if (/^[A-Za-z]{2}$/.test(t) && CODES.has(t.toUpperCase())) return t.toUpperCase();
  const named = countryByName(t);
  if (named) return named;
  const bracketed = /^([^()]+)\(([^()]+)\)$/.exec(t);
  if (!bracketed) return '';
  const [outer, inner] = [countryByName(bracketed[1]), countryByName(bracketed[2])];
  return outer && (!inner || inner === outer) ? outer : '';
}

// For a condition that compares a country item with a typed value: null when the value needs
// no remark (another item, or already a code), else { code } with the code the value names
// ('' when it names no country). Stored values are codes, so "같음 미국" would match no one.
export function countryHint(dictionary, key, value) {
  const text = String(value ?? '').trim();
  if (!text || standardByKey(dictionary, String(key ?? '').trim())?.kind !== 'country') return null;
  const code = countryCode(text);
  return code === text ? null : { code };
}

// Moves the decimal point of a plain decimal text right by exp places and rounds half up.
function scaled(text, exp) {
  const [int, fraction = ''] = text.split('.');
  const digits = int + fraction.padEnd(exp, '0');
  const whole = digits.slice(0, int.length + exp).replace(/^0+(?=\d)/, '');
  if (whole.length > 15) return null;
  return Number(whole || '0') + (digits[int.length + exp] >= '5' ? 1 : 0);
}

// Thousands commas first: the Korean parts are matched without an end anchor.
const DECIMAL = '(\\d{1,3}(?:,\\d{3})+|\\d+)(\\.\\d+)?';
const plain = (int, fraction = '') => `${int.replace(/,/g, '')}${fraction}`;
const PLAIN_NUMBER = new RegExp(`^${DECIMAL}$`);
const UNIT_NUMBER = new RegExp(`^${DECIMAL}([kmb])$`, 'i');
const KOREAN_PART = new RegExp(`${DECIMAL}([천백십]?)`, 'gy');
const UNITS = {
  k: 3, m: 6, b: 9, 십: 1, 백: 2, 천: 3,
};

// A count below 10,000 in Korean units: "3.5천", "2천5백", "500". null when it is not one.
function koreanSmall(text) {
  if (!text) return 0;
  let sum = 0;
  KOREAN_PART.lastIndex = 0;
  for (let at = 0; at < text.length; at = KOREAN_PART.lastIndex) {
    const part = KOREAN_PART.exec(text);
    if (!part) return null;
    const value = scaled(plain(part[1], part[2]), UNITS[part[3]] || 0);
    if (value === null) return null;
    sum += value;
  }
  return sum;
}

// A count written with 억/만/천: "1만", "1.2만", "3.5천", "1만 2천", "2천만", "1억 2천만".
function koreanNumber(text) {
  const parts = /^(?:(.+)억)?(?:(.+?)만)?(.*)$/.exec(text);
  if (!parts || !/[억만천백십]/.test(text)) return null;
  // A decimal in front of a big unit ("1.2만") scales with it.
  const big = (part, exp) => {
    if (part === undefined) return 0;
    const single = PLAIN_NUMBER.exec(part);
    if (single) return scaled(plain(single[1], single[2]), exp);
    const small = koreanSmall(part);
    return small === null ? null : small * 10 ** exp;
  };
  const values = [big(parts[1], 8), big(parts[2], 4), koreanSmall(parts[3])];
  return values.includes(null) ? null : values[0] + values[1] + values[2];
}

// The whole number of 0 or more a text writes, or null: digits with or without thousands
// commas, a K/M/B suffix (10K, 1.2M) or Korean units (1만, 3.5천), a trailing "명" aside.
// A fraction is rounded only where a unit explains it; a bare "12.500" may be 12,500 in
// another country's notation, so it is not a number here ("14900.0" still is).
export function wholeNumber(text) {
  const t = String(text ?? '').replace(/\s+/g, '').replace(/명$/, '');
  let value = null;
  const bare = PLAIN_NUMBER.exec(t);
  const unit = UNIT_NUMBER.exec(t);
  if (bare) value = /^(\.0+)?$/.test(bare[2] || '') ? scaled(plain(bare[1]), 0) : null;
  else if (unit) value = scaled(plain(unit[1], unit[2]), UNITS[unit[3].toLowerCase()]);
  else value = koreanNumber(t);
  return value !== null && Number.isSafeInteger(value) ? value : null;
}

// Brings one cell into the format of a standard item's kind. status is 'kept' (stored as
// written), 'changed' (stored as value), 'blank' (means "not known": the item is left out)
// or 'unreadable' (cannot be stored under this item as it is).
export function convertValue(kind, raw) {
  const text = String(raw ?? '').trim();
  if (kind !== 'country' && kind !== 'integer') return { status: text ? 'kept' : 'blank', value: text };
  if (isBlank(text)) return { status: 'blank', value: '' };
  if (kind === 'country') {
    const code = countryCode(text);
    if (!code) return { status: 'unreadable', value: text };
    return { status: code === text ? 'kept' : 'changed', value: code };
  }
  const number = wholeNumber(text);
  if (number === null) return { status: 'unreadable', value: text };
  // A JSON number is stored, so the text always changes form; digits alone count as kept.
  return { status: String(number) === text ? 'kept' : 'changed', value: number };
}
