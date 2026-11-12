import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import {
  filterToForm, formatNumberText, formToFilter, koreanNumberText, newDynamicFilter, parseNumberText, summarizeFilter,
  validateDynamicForm,
} from '../src/dynamic-list.js';

// The real language files, so the sentences below are the ones the screen shows.
const language = (lang) => {
  const messages = JSON.parse(readFileSync(new URL(`../../i18n/${lang}.json`, import.meta.url), 'utf8'));
  const t = (key, params = {}) => {
    assert.ok(key in messages, `missing ${lang} message ${key}`);
    return messages[key].replace(/\{(\w+)\}/g, (_, name) => params[name] ?? '');
  };
  return [t, (key) => key in messages];
};

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

test('summarizes the base and conditions as one Korean sentence', () => {
  const [t, te] = language('ko');
  const say = (form, lists = {}) => summarizeFilter(form, lists, t, te);
  const form = filterToForm({
    version: 1,
    base: { type: 'all' },
    match: 'all',
    conditions: [{ key: 'grade', op: 'eq', value: 'gold' }, { key: 'purchase_amount', op: 'gte', value: '100000' }],
  });
  assert.equal(say(form), "전체 구독자 중 유효한 구독이 있고, grade 값이 'gold'이고, purchase_amount 값이 100,000 이상인 사람");
  form.match = 'any';
  assert.equal(say(form), "전체 구독자 중 유효한 구독이 있고, grade 값이 'gold'이거나, purchase_amount 값이 100,000 이상인 사람 (조건 중 하나만 맞아도 포함)");

  const each = {
    eq: [{ value: 'gold' }, "x 값이 'gold'인"],
    contains: [{ value: 'go' }, "x 값에 'go' 글자가 들어 있는"],
    empty: [{}, 'x 값이 비어 있는'],
    has: [{ value: 'vip' }, "x 태그에 'vip' 태그가 있는"],
    gte: [{ value: '100000' }, 'x 값이 100,000 이상인'],
    gt: [{ value: '1' }, 'x 값이 1 초과인'],
    lte: [{ value: '2.5' }, 'x 값이 2.5 이하인'],
    lt: [{ value: '100,000' }, 'x 값이 100,000 미만인'],
    between: [{ value: '50000', value2: '100000' }, 'x 값이 50,000 이상 100,000 미만인'],
  };
  Object.entries(each).forEach(([op, [values, clause]]) => {
    form.match = 'all';
    form.conditions = [{ key: ' x ', op, ...values }];
    assert.equal(say(form), `전체 구독자 중 유효한 구독이 있고, ${clause} 사람`, op);
  });
  form.conditions = [{ key: 'a', op: 'empty' }, { key: 'b', op: 'contains', value: 'x' }, { key: 'c', op: 'eq', value: 'y' }];
  assert.equal(say(form), "전체 구독자 중 유효한 구독이 있고, a 값이 비어 있고, b 값에 'x' 글자가 들어 있고, c 값이 'y'인 사람");
  form.match = 'any';
  assert.equal(say(form), "전체 구독자 중 유효한 구독이 있고, a 값이 비어 있거나, b 값에 'x' 글자가 들어 있거나, c 값이 'y'인 사람 (조건 중 하나만 맞아도 포함)");

  // Base lists are named; rows still being typed are left out; nothing usable gives no sentence.
  form.match = 'all';
  form.base = { type: 'lists', lists: [3, 9] };
  form.conditions = [{ key: 'grade', op: 'eq', value: 'gold' }, { key: '', op: 'eq', value: 'x' },
    { key: 'amount', op: 'gte', value: '100,000원' }, { key: 'amount', op: 'between', value: '1', value2: '' }];
  assert.equal(say(form, { 3: '뉴스레터', 9: 'VIP' }), "뉴스레터, VIP 리스트에 유효한 구독이 있고, grade 값이 'gold'인 사람");
  assert.equal(say(form, {}), '');
  form.base = { type: 'all' };
  form.conditions = [{ key: 'grade', op: 'eq', value: ' ' }];
  assert.equal(say(form), '');
});

test('summarizes in English without Korean endings', () => {
  const [t, te] = language('en');
  const form = filterToForm({
    version: 1,
    base: { type: 'lists', lists: [3] },
    match: 'all',
    conditions: [{ key: 'grade', op: 'eq', value: 'gold' }, { key: 'amount', op: 'between', value: '50000', value2: '100000' },
      { key: 'tags', op: 'has', value: 'vip' }, { key: 'note', op: 'empty' }],
  });
  assert.equal(summarizeFilter(form, { 3: 'News' }, t, te), "Subscribers with a valid subscription to News whose grade is 'gold'"
    + " and amount is 50,000 or more and less than 100,000 and tags has the tag 'vip' and note is empty.");
  form.match = 'any';
  form.base = { type: 'all' };
  form.conditions = [{ key: 'a', op: 'lt', value: '5' }, { key: 'b', op: 'contains', value: 'x' }];
  assert.equal(summarizeFilter(form, {}, t, te), "All subscribers with a valid subscription whose a is less than 5 or b contains 'x'"
    + ' (any one condition is enough).');
});
