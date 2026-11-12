// Attribute ("추가 정보") columns and conditions of the subscribers table. Everything here
// works on the attribs the subscribers API already returns; nothing asks the server for more.
// The extension lets the Node test runner load this module without a bundler.
import {
  formToFilter, formatNumberText, isNumberOp, validateDynamicForm,
} from './dynamic-list.js'; // eslint-disable-line import/extensions

export const MAX_ATTRIB_COLUMNS = 8;
export const SUGGESTED_COLUMNS = 3;
export const MAX_CONDITIONS = 20;
// Base columns that can be hidden. E-mail and name always stay.
export const BASE_COLUMNS = ['lists', 'created_at', 'updated_at'];
// "between" is left out: two conditions (이상 + 미만) say the same, as all conditions must match.
export const FILTER_OPS = { text: ['eq', 'contains', 'empty', 'has'], number: ['gte', 'gt', 'lte', 'lt'] };
// Values longer than this are cut in the table and can be opened in place.
export const LONG_VALUE = 60;

const STORAGE_KEY = 'axm.subscribers.columns.v1';

const isBlank = (v) => v === undefined || v === null
  || (typeof v === 'string' && v.trim() === '') || (Array.isArray(v) && v.length === 0);

const plainObject = (v) => v !== null && typeof v === 'object' && !Array.isArray(v);

// Shows a value the way the CSV export does: text as it is, numbers and booleans as written,
// a list as its items joined by ", ", an object as compact JSON. '' when there is no value.
export function attribText(value) {
  if (isBlank(value)) return '';
  if (Array.isArray(value)) {
    return value.map((item) => (item !== null && typeof item === 'object' ? JSON.stringify(item) : attribText(item)))
      .join(', ');
  }
  if (typeof value === 'object') return JSON.stringify(value);
  return String(value);
}

// Counts, per attribute key, the subscribers of the page that have a value. Keys keep the
// order in which they first appear.
export function attribStats(subscribers) {
  const filled = new Map();
  (subscribers || []).forEach((subscriber) => {
    if (!plainObject(subscriber.attribs)) return;
    Object.entries(subscriber.attribs).forEach(([key, value]) => {
      filled.set(key, (filled.get(key) || 0) + (isBlank(value) ? 0 : 1));
    });
  });
  return [...filled].map(([key, count]) => ({ key, filled: count }));
}

// The columns shown before the user has picked any: the keys most subscribers of the page
// have a value for. Ties keep the first-seen order (Array.prototype.sort is stable).
export function suggestColumns(stats, max = SUGGESTED_COLUMNS) {
  return stats.filter((s) => s.filled > 0).sort((a, b) => b.filled - a.filled).slice(0, max).map((s) => s.key);
}

const validKey = (key) => typeof key === 'string' && key.trim() === key && [...key].length >= 1
  && [...key].length <= 64 && ![...key].some((c) => c.codePointAt(0) < 32 || (c.codePointAt(0) >= 127 && c.codePointAt(0) <= 159));

// Trims a typed key; '' when it cannot be an attribute key.
export function cleanKey(text) {
  const key = String(text ?? '').trim();
  return validKey(key) ? key : '';
}

const readAll = (storage) => {
  try {
    const all = JSON.parse(storage.getItem(STORAGE_KEY) || '{}');
    return plainObject(all) ? all : {};
  } catch {
    return {};
  }
};

// Reads the columns saved for one list ('all' for the page without a list), or null.
export function loadColumns(storage, listKey) {
  const saved = readAll(storage)[String(listKey)];
  if (!plainObject(saved) || !Array.isArray(saved.attribs) || !Array.isArray(saved.hidden)) return null;
  return {
    attribs: [...new Set(saved.attribs.filter(validKey))].slice(0, MAX_ATTRIB_COLUMNS),
    hidden: BASE_COLUMNS.filter((id) => saved.hidden.includes(id)),
  };
}

// Saves the columns of one list, or forgets them when columns is null. A browser that blocks
// storage simply does not remember.
export function saveColumns(storage, listKey, columns) {
  try {
    const all = readAll(storage);
    if (columns) all[String(listKey)] = { attribs: columns.attribs, hidden: columns.hidden };
    else delete all[String(listKey)];
    storage.setItem(STORAGE_KEY, JSON.stringify(all));
  } catch { /* Not remembered. */ }
}

export const newCondition = (key = '') => ({ key, op: 'eq', value: '' });

const asForm = (condition) => ({ base: { type: 'all' }, match: 'all', conditions: [condition] });

// '' or the error code of the dynamic list form (keyRequired, valueRequired, numberRequired).
export const conditionError = (condition) => validateDynamicForm(asForm(condition));

// The condition as the server reads it (numbers without thousands commas, no value for empty).
export const cleanCondition = (condition) => formToFilter(asForm(condition)).conditions[0];

// The value as shown on a condition chip.
export const conditionValueText = (condition) => (isNumberOp(condition.op)
  ? formatNumberText(condition.value) : String(condition.value ?? '').trim());
