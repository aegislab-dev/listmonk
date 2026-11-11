import test from 'node:test';
import assert from 'node:assert/strict';
import { filterToForm, formToFilter, newDynamicFilter, validateDynamicForm } from '../src/dynamic-list.js';

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
