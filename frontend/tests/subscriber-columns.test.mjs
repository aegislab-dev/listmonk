import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import {
  FILTER_OPS, LONG_VALUE, attribKeys, attribText, cleanCondition, conditionError, conditionValueText, newCondition,
  presetOf,
} from '../src/subscriber-columns.js';

const messages = (lang) => JSON.parse(readFileSync(new URL(`../../i18n/${lang}.json`, import.meta.url), 'utf8'));

test('shows attribute values the way the CSV export does', () => {
  assert.equal(attribText('PH'), 'PH');
  assert.equal(attribText(' 12,500 '), ' 12,500 ');
  assert.equal(attribText(12500), '12500');
  assert.equal(attribText(0), '0');
  assert.equal(attribText(false), 'false');
  assert.equal(attribText(['vip', 'tech']), 'vip, tech');
  assert.equal(attribText(['a', 1, null, { b: 1 }, [2]]), 'a, 1, , {"b":1}, [2]');
  assert.equal(attribText({ city: 'Cebu', zip: '6000' }), '{"city":"Cebu","zip":"6000"}');
});

test('a missing, null or blank value has no text, so the table shows a dash', () => {
  [undefined, null, '', '   ', []].forEach((value) => assert.equal(attribText(value), ''));
  // A subscriber that has region but no country yet.
  const row = { attribs: { region: 'PH', follower: '1200' } };
  assert.equal(attribText(row.attribs.country), '');
  assert.equal(attribText(row.attribs.follower), '1200');
});

test('long values are the ones that get a "more" toggle', () => {
  assert.equal('가'.repeat(LONG_VALUE).length > LONG_VALUE, false);
  assert.equal('가'.repeat(LONG_VALUE + 1).length > LONG_VALUE, true);
});

test('collects the attribute keys of the page in first-seen order', () => {
  assert.deepEqual(attribKeys([
    { attribs: { country: 'PH', follower: '1' } },
    { attribs: { region: 'PH', country: '' } },
    { attribs: null }, { attribs: 'text' }, { attribs: ['a'] }, {},
  ]), ['country', 'follower', 'region']);
  assert.deepEqual(attribKeys(undefined), []);
});

test('a list shows its preset, and falls back to the default one', () => {
  const presets = [{ id: 'creator', columns: ['country', 'follower'] }, { id: 'b2b', columns: ['company'] }];
  assert.deepEqual(presetOf(presets, 'b2b', 'creator').columns, ['company']);
  assert.deepEqual(presetOf(presets, 'creator', 'creator').columns, ['country', 'follower']);
  // No list (all subscribers), an unknown preset, or a list loaded before its preset.
  assert.equal(presetOf(presets, '', 'creator').id, 'creator');
  assert.equal(presetOf(presets, 'gone', 'creator').id, 'creator');
  assert.equal(presetOf(presets, undefined, 'creator').id, 'creator');
  // Before the server config has loaded there are simply no attribute columns.
  assert.deepEqual(presetOf(undefined, 'creator', 'creator'), { id: '', columns: [] });
  assert.deepEqual(presetOf([], '', undefined).columns, []);
});

test('conditions are checked and cleaned like dynamic list conditions', () => {
  assert.deepEqual(newCondition('country'), { key: 'country', op: 'eq', value: '' });
  assert.equal(conditionError({ key: '', op: 'eq', value: 'PH' }), 'keyRequired');
  assert.equal(conditionError({ key: 'country', op: 'eq', value: '  ' }), 'valueRequired');
  assert.equal(conditionError({ key: 'follower', op: 'gte', value: '100,000원' }), 'numberRequired');
  assert.equal(conditionError({ key: 'follower', op: 'gte', value: '100,000' }), '');
  assert.equal(conditionError({ key: 'memo', op: 'empty', value: '' }), '');
  assert.equal(conditionError({ key: 'x'.repeat(65), op: 'empty' }), 'keyRequired');

  assert.deepEqual(cleanCondition({ key: ' country ', op: 'eq', value: ' PH ' }), { key: 'country', op: 'eq', value: 'PH' });
  assert.deepEqual(cleanCondition({ key: 'follower', op: 'gte', value: ' 500,000 ' }), { key: 'follower', op: 'gte', value: '500000' });
  assert.deepEqual(cleanCondition({ key: 'memo', op: 'empty', value: 'ignored' }), { key: 'memo', op: 'empty' });
});

test('condition chips show numbers with thousands commas', () => {
  assert.equal(conditionValueText({ key: 'follower', op: 'gte', value: '500000' }), '500,000');
  assert.equal(conditionValueText({ key: 'country', op: 'eq', value: ' PH ' }), 'PH');
  assert.equal(conditionValueText({ key: 'memo', op: 'empty' }), '');
});

test('every condition and preset the screen can show has its messages', () => {
  const ops = [...FILTER_OPS.text, ...FILTER_OPS.number];
  assert.deepEqual(ops, ['eq', 'contains', 'empty', 'has', 'gte', 'gt', 'lte', 'lt']);
  ['ko', 'en'].forEach((lang) => {
    const m = messages(lang);
    ops.forEach((op) => {
      assert.ok(m[`subscribers.columns.chip.${op}`].includes('{key}'), `${lang} chip ${op}`);
      assert.ok(m[`lists.dynamic.ops.${op}`], `${lang} op ${op}`);
    });
    ['keyRequired', 'valueRequired', 'numberRequired'].forEach((code) => assert.ok(m[`lists.dynamic.${code}`], `${lang} ${code}`));
    assert.ok(m['lists.preset.creator'], `${lang} preset name`);
  });
  assert.equal(messages('ko')['subscribers.columns.chip.gte'].replace('{key}', 'follower').replace('{value}', '500,000'), 'follower 500,000 이상');
});
