import test from 'node:test';
import assert from 'node:assert/strict';
import {
  filterToForm, formatNumberText, formToFilter, koreanNumberText, newDynamicFilter, parseNumberText, validateDynamicForm,
} from '../src/dynamic-list.js';

const valid = () => ({
  version: 1,
  base: { type: 'lists', lists: [2, 1, 2] },
  match: 'any',
  conditions: [
    { key: ' plan ', op: 'eq', value: ' pro ' },
    { key: 'tags', op: 'has', value: 'vip' },
  ],
});

test('new filters default to a guarded all-list rule with one editable condition', () => {
  assert.deepEqual(newDynamicFilter(), {
    version: 1, base: { type: 'all' }, match: 'all', conditions: [{ key: '', op: 'eq', value: '' }],
  });
});

test('form conversion trims values, deduplicates source IDs and omits empty operator values', () => {
  const form = filterToForm(valid());
  const converted = formToFilter(form);
  assert.deepEqual(converted, {
    version: 1,
    base: { type: 'lists', lists: [2, 1] },
    match: 'any',
    conditions: [{ key: 'plan', op: 'eq', value: 'pro' }, { key: 'tags', op: 'has', value: 'vip' }],
  });
  form.conditions[0].op = 'empty';
  assert.deepEqual(formToFilter(form).conditions[0], { key: 'plan', op: 'empty' });
});

test('form conversion round-trips the persisted format without aliasing nested data', () => {
  const raw = valid();
  const clone = filterToForm(raw);
  clone.base.lists.push(7);
  assert.deepEqual(raw.base.lists, [2, 1, 2]);
  assert.deepEqual(formToFilter(filterToForm(raw)).conditions, [
    { key: 'plan', op: 'eq', value: 'pro' }, { key: 'tags', op: 'has', value: 'vip' },
  ]);
});

test('validates base, match, condition counts, keys, operators and values', () => {
  const f = filterToForm(valid());
  assert.equal(validateDynamicForm(f), '');
  f.base.lists = [];
  assert.equal(validateDynamicForm(f), 'baseRequired');
  f.base.lists = [1];
  f.match = 'xor';
  assert.equal(validateDynamicForm(f), 'invalid');
  f.match = 'all';
  f.conditions = [];
  assert.equal(validateDynamicForm(f), 'conditionsRequired');
  f.conditions = [{ key: 'a'.repeat(65), op: 'eq', value: 'x' }];
  assert.equal(validateDynamicForm(f), 'keyRequired');
  f.conditions = [{ key: 'a\n', op: 'eq', value: 'x' }];
  assert.equal(validateDynamicForm(f), 'keyRequired');
  f.conditions = [{ key: 'a', op: 'sql', value: 'x' }];
  assert.equal(validateDynamicForm(f), 'invalid');
  f.conditions = [{ key: 'a', op: 'eq', value: '  ' }];
  assert.equal(validateDynamicForm(f), 'valueRequired');
  f.conditions = [{ key: 'a', op: 'empty', value: '' }];
  assert.equal(validateDynamicForm(f), '');
});

test('enforces max filter sizes and rejects unsafe base IDs and NUL values', () => {
  const f = filterToForm(valid());
  f.base.lists = Array.from({ length: 21 }, (_, i) => i + 1);
  assert.equal(validateDynamicForm(f), 'baseRequired');
  f.base.lists = [0];
  assert.equal(validateDynamicForm(f), 'baseRequired');
  f.base = { type: 'all' };
  f.conditions = Array.from({ length: 21 }, () => ({ key: 'x', op: 'empty' }));
  assert.equal(validateDynamicForm(f), 'conditionsRequired');
  f.conditions = [{ key: 'x', op: 'eq', value: `a\0b` }];
  assert.equal(validateDynamicForm(f), 'valueRequired');
  f.conditions = [{ key: 'x', op: 'eq', value: 'a'.repeat(201) }];
  assert.equal(validateDynamicForm(f), 'valueRequired');
});

test('reads, formats and names numbers by the server rule', () => {
  const cases = {
    100000: '100000', ' 100,000 ': '100000', '1e5': '1e5', '1E+05': '1E+05', '+1,234.50': '+1234.50',
    '-0.5': '-0.5', '.5': '.5', '5.': '5.', '12,345,678': '12345678',
  };
  Object.entries(cases).forEach(([text, want]) => assert.equal(parseNumberText(text), want, text));
  ['100,000원', '₩100,000', '10만', '1,00', '12,34', '1,2345', 'abc', '1e999', '１００', '1 000', 'NaN', 'Infinity',
    '0x10', '--1', '1e', '.', '', '50%', '1'.repeat(65), null, undefined]
    .forEach((text) => assert.equal(parseNumberText(text), null, String(text)));

  assert.equal(formatNumberText('100000'), '100,000');
  assert.equal(formatNumberText(' 1234567.8900 '), '1,234,567.8900');
  assert.equal(formatNumberText('-1,000'), '-1,000');
  assert.equal(formatNumberText('+999'), '999');
  assert.equal(formatNumberText('.5'), '.5');
  assert.equal(formatNumberText('1e5'), '1e5');
  assert.equal(formatNumberText(' gold '), 'gold');
  assert.equal(formatNumberText(undefined), '');

  assert.equal(koreanNumberText('100,000'), '10만');
  assert.equal(koreanNumberText('100000.00'), '10만');
  assert.equal(koreanNumberText('123456789'), '1억 2,345만 6,789');
  assert.equal(koreanNumberText('-50000'), '-5만');
  assert.equal(koreanNumberText('1000000000000'), '1조');
  assert.equal(koreanNumberText('9999'), '');
  assert.equal(koreanNumberText('100000.5'), '');
  assert.equal(koreanNumberText('1e5'), '');
  assert.equal(koreanNumberText('gold'), '');
});

test('number conditions convert between input text and the saved filter', () => {
  const form = filterToForm({
    version: 1,
    base: { type: 'all' },
    match: 'all',
    conditions: [
      { key: 'amount', op: 'gte', value: '100000' },
      { key: 'amount', op: 'between', value: '50000', value2: '1234567.5' },
      { key: 'plan', op: 'eq', value: '100000' },
    ],
  });
  assert.deepEqual(form.conditions.map((c) => [c.value, c.value2]), [
    ['100,000', undefined], ['50,000', '1,234,567.5'], ['100000', undefined],
  ]);
  form.conditions[0].value = ' 2,500,000 ';
  assert.deepEqual(formToFilter(form).conditions, [
    { key: 'amount', op: 'gte', value: '2500000' },
    { key: 'amount', op: 'between', value: '50000', value2: '1234567.5' },
    { key: 'plan', op: 'eq', value: '100000' },
  ]);
  // A second value is sent only for "between".
  form.conditions[1].op = 'lt';
  assert.deepEqual(formToFilter(form).conditions[1], { key: 'amount', op: 'lt', value: '50000' });
});

test('validates number conditions and the between range', () => {
  const f = filterToForm(valid());
  f.conditions = [{ key: 'amount', op: 'gte', value: '100,000' }];
  assert.equal(validateDynamicForm(f), '');
  ['', '  ', '100,000원', '1,00', 'abc'].forEach((value) => {
    f.conditions = [{ key: 'amount', op: 'lt', value }];
    assert.equal(validateDynamicForm(f), 'numberRequired', value);
  });
  f.conditions = [{ key: 'amount', op: 'between', value: '50,000', value2: '100,000' }];
  assert.equal(validateDynamicForm(f), '');
  f.conditions = [{ key: 'amount', op: 'between', value: '50,000', value2: '' }];
  assert.equal(validateDynamicForm(f), 'numberRequired');
  f.conditions = [{ key: 'amount', op: 'between', value: '50,000' }];
  assert.equal(validateDynamicForm(f), 'numberRequired');
  f.conditions = [{ key: 'amount', op: 'between', value: '100000', value2: '100,000' }];
  assert.equal(validateDynamicForm(f), 'rangeInvalid');
  f.conditions = [{ key: 'amount', op: 'between', value: '2', value2: '1' }];
  assert.equal(validateDynamicForm(f), 'rangeInvalid');
  // Text conditions keep accepting any text, including numbers with units.
  f.conditions = [{ key: 'amount', op: 'eq', value: '100,000원' }];
  assert.equal(validateDynamicForm(f), '');
});
