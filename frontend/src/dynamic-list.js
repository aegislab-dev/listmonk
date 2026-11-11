// Versioned JSON data only; this module never builds SQL.
const hasControls = (key) => [...key].some((char) => {
  const code = char.codePointAt(0);
  return code < 32 || (code >= 127 && code <= 159);
});
export function newDynamicFilter() {
  return {
    version: 1,
    base: { type: 'all' },
    match: 'all',
    conditions: [{ key: '', op: 'eq', value: '' }],
  };
}

export function filterToForm(filter) {
  return JSON.parse(JSON.stringify(filter || newDynamicFilter()));
}

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
      ...(c.op === 'empty' ? {} : { value: (c.value || '').trim() }),
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
  if (form.conditions.some((c) => !['eq', 'contains', 'empty', 'has'].includes(c.op))) return 'invalid';
  if (form.conditions.some((c) => c.op !== 'empty' && (!(c.value || '').trim()
    || [...c.value.trim()].length > 200 || c.value.includes('\0')))) return 'valueRequired';
  return '';
}
