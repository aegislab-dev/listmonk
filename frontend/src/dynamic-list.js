// Versioned JSON data only; this module never builds SQL.
const hasControls = (key) => [...key].some((char) => {
  const code = char.codePointAt(0);
  return code < 32 || (code >= 127 && code <= 159);
});
export const TEXT_OPS = ['eq', 'contains', 'empty', 'has'];
export const NUMBER_OPS = ['gte', 'gt', 'lte', 'lt', 'between'];
export const isNumberOp = (op) => NUMBER_OPS.includes(op);

// The server's number rule (axm_to_number): a plain decimal with an optional two-digit
// exponent, or a decimal with thousands commas in groups of three. Up to 64 characters.
const PLAIN_NUMBER = /^[+-]?(\d+\.?\d*|\.\d+)([eE][+-]?\d{1,2})?$/;
const GROUPED_NUMBER = /^[+-]?\d{1,3}(,\d{3})+(\.\d*)?$/;

// Returns the number without thousands commas, or null when the text is not a number.
export function parseNumberText(text) {
  const t = String(text ?? '').trim();
  if (t.length > 64) return null;
  if (GROUPED_NUMBER.test(t)) return t.replace(/,/g, '');
  return PLAIN_NUMBER.test(t) ? t : null;
}

// Shows a number with thousands commas (100000 -> 100,000). Other text is returned trimmed.
export function formatNumberText(text) {
  const n = parseNumberText(text);
  const parts = n === null ? null : /^([+-]?)(\d*)(\.\d*)?$/.exec(n);
  if (!parts) return String(text ?? '').trim();
  const [, sign, int, fraction = ''] = parts;
  return `${sign === '-' ? '-' : ''}${int.replace(/\B(?=(\d{3})+$)/g, ',')}${fraction}`;
}

// Reads a whole number of 10,000 or more in Korean units (100000 -> 10만). '' otherwise.
export function koreanNumberText(text) {
  const n = parseNumberText(text);
  const parts = n === null ? null : /^([+-]?)0*(\d{5,16})\.?0*$/.exec(n);
  if (!parts) return '';
  const groups = [];
  for (let digits = parts[2]; digits; digits = digits.slice(0, -4)) groups.push(Number(digits.slice(-4)));
  const words = groups.map((g, i) => (g ? `${g.toLocaleString('en-US')}${['', '만', '억', '조'][i]}` : ''));
  return `${parts[1] === '-' ? '-' : ''}${words.reverse().filter(Boolean).join(' ')}`;
}

export function newDynamicFilter() {
  return {
    version: 1,
    base: { type: 'all' },
    match: 'all',
    conditions: [{ key: '', op: 'eq', value: '' }],
  };
}

// The form keeps number conditions as the text shown in the input (with thousands commas).
export function filterToForm(filter) {
  const form = JSON.parse(JSON.stringify(filter || newDynamicFilter()));
  form.conditions = form.conditions.map((c) => (isNumberOp(c.op) ? {
    ...c,
    value: formatNumberText(c.value),
    ...(c.op === 'between' ? { value2: formatNumberText(c.value2) } : {}),
  } : c));
  return form;
}

const conditionValue = (op, value) => {
  const text = (value || '').trim();
  return isNumberOp(op) ? parseNumberText(text) ?? text : text;
};

export function formToFilter(form) {
  return {
    version: 1,
    base: form.base.type === 'lists'
      ? { type: 'lists', lists: [...new Set(form.base.lists || [])] }
      : { type: 'all' },
    match: form.match,
    conditions: form.conditions.map((c) => ({
      key: c.key.trim(),
      op: c.op,
      ...(c.op === 'empty' ? {} : { value: conditionValue(c.op, c.value) }),
      ...(c.op === 'between' ? { value2: conditionValue(c.op, c.value2) } : {}),
    })),
  };
}

export function validateDynamicForm(form) {
  if (!['all', 'lists'].includes(form.base.type) || !['all', 'any'].includes(form.match)) return 'invalid';
  if (form.base.type === 'lists' && (!(form.base.lists || []).length
    || form.base.lists.length > 20 || form.base.lists.some((id) => !Number.isInteger(id) || id < 1))) return 'baseRequired';
  if (!form.conditions.length || form.conditions.length > 20) return 'conditionsRequired';
  if (form.conditions.some((c) => !c.key.trim() || [...c.key.trim()].length > 64
    || hasControls(c.key))) return 'keyRequired';
  if (form.conditions.some((c) => !TEXT_OPS.includes(c.op) && !isNumberOp(c.op))) return 'invalid';
  if (form.conditions.some((c) => TEXT_OPS.includes(c.op) && c.op !== 'empty' && (!(c.value || '').trim()
    || [...c.value.trim()].length > 200 || c.value.includes('\0')))) return 'valueRequired';
  if (form.conditions.some((c) => isNumberOp(c.op) && (parseNumberText(c.value) === null
    || (c.op === 'between' && parseNumberText(c.value2) === null)))) return 'numberRequired';
  // An approximate check for the message; the server compares the two bounds exactly.
  if (form.conditions.some((c) => c.op === 'between'
    && !(Number(parseNumberText(c.value)) < Number(parseNumberText(c.value2))))) return 'rangeInvalid';
  return '';
}
