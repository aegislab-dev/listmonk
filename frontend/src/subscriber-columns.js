// Attribute ("추가 정보") columns and conditions of the subscribers table. The columns come
// from the list's column preset and show the attribs the subscribers API already returns.
// The extension lets the Node test runner load this module without a bundler.
import {
  formToFilter, formatNumberText, isNumberOp, validateDynamicForm,
} from './dynamic-list.js'; // eslint-disable-line import/extensions

export const MAX_CONDITIONS = 20;
// "between" is left out: two conditions (이상 + 미만) say the same, as all conditions must match.
export const FILTER_OPS = { text: ['eq', 'contains', 'empty', 'has'], number: ['gte', 'gt', 'lte', 'lt'] };
// Values longer than this are cut in the table and can be opened in place.
export const LONG_VALUE = 60;

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

// The attribute keys the subscribers of the page have, in first-seen order.
export function attribKeys(subscribers) {
  const keys = new Set();
  (subscribers || []).forEach((subscriber) => {
    if (plainObject(subscriber.attribs)) Object.keys(subscriber.attribs).forEach((key) => keys.add(key));
  });
  return [...keys];
}

// The attribute columns of a list: those of its preset or, for an unknown preset and for the
// page without a list, those of the default preset. presets is [{ id, columns }].
export function presetOf(presets, id, defaultId) {
  const all = presets || [];
  return all.find((p) => p.id === id) || all.find((p) => p.id === defaultId) || { id: '', columns: [] };
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
