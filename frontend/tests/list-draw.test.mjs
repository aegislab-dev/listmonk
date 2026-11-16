import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import {
  defaultDrawName, drawBody, DRAW_MAX_COUNT, DRAW_TAG, localDate, recordSettings, validateDraw, willDraw,
} from '../src/list-draw.js';

const form = (extra = {}) => ({
  unit: 'count', value: '10', scope: 'same_source', period: 'all', days: '', ...extra,
});

test('a count or percent must be a whole number in range', () => {
  assert.equal(validateDraw(form()), '');
  assert.equal(validateDraw(form({ value: ' 1 ' })), '');
  assert.equal(validateDraw(form({ value: String(DRAW_MAX_COUNT) })), '');
  for (const value of ['', '0', '-1', '1.5', '1e3', 'abc', '१०', String(DRAW_MAX_COUNT + 1), undefined]) {
    assert.equal(validateDraw(form({ value })), 'valueRequired', String(value));
  }
  assert.equal(validateDraw(form({ unit: 'percent', value: '100' })), '');
  assert.equal(validateDraw(form({ unit: 'percent', value: '101' })), 'valueRequired');
  assert.equal(validateDraw(form({ unit: 'percent', value: '0' })), 'valueRequired');
});

test('the look back is needed only for "recent days"', () => {
  assert.equal(validateDraw(form({ period: 'days', days: '7' })), '');
  for (const days of ['', '0', '-2', '3.5', '3651', 'x']) {
    assert.equal(validateDraw(form({ period: 'days', days })), 'daysRequired', days);
  }
  assert.equal(validateDraw(form({ period: 'all', days: 'junk' })), '');
  assert.equal(validateDraw(form({ scope: 'mine' })), 'invalid');
});

test('the request body follows the server API', () => {
  assert.deepEqual(drawBody(form()), { unit: 'count', value: 10, exclude_scope: 'same_source', exclude_days: 0 });
  assert.deepEqual(drawBody(form({ unit: 'percent', value: ' 25 ', scope: 'all', period: 'days', days: '7' })),
    { unit: 'percent', value: 25, exclude_scope: 'all', exclude_days: 7 });
  // The days stay at 0 (all the time) unless the period is "recent days".
  assert.equal(drawBody(form({ period: 'all', days: '9' })).exclude_days, 0);
});

test('the default name is "source · date · count" and fits the name limit', () => {
  assert.equal(defaultDrawName('미국 크리에이터', '2026-11-16 · 10,000명'), '미국 크리에이터 · 2026-11-16 · 10,000명');
  const long = defaultDrawName('가'.repeat(250), '2026-11-16 · 3명');
  assert.equal([...long].length, 200);
  assert.ok(long.endsWith(' · 2026-11-16 · 3명'));
  assert.equal(defaultDrawName('S', 'x'), 'S · x');
});

test('the local date is year-month-day with zeros', () => {
  assert.equal(localDate(new Date(2026, 0, 5)), '2026-01-05');
  assert.equal(localDate(new Date(2026, 10, 16, 23, 59)), '2026-11-16');
});

test('what is drawn is the smaller of the request and the candidates', () => {
  assert.equal(willDraw(null), 0);
  assert.equal(willDraw({ requested: 1000, candidates: 400 }), 400);
  assert.equal(willDraw({ requested: 10, candidates: 400 }), 10);
  assert.equal(willDraw({ requested: 10, candidates: 0 }), 0);
});

test('stored settings are read with safe defaults', () => {
  assert.deepEqual(recordSettings({ unit: 'percent', value: 10, exclude_scope: 'all', exclude_days: 7 }),
    { unit: 'percent', value: 10, scope: 'all', days: 7 });
  assert.deepEqual(recordSettings({ unit: 'count', value: 5, excludeScope: 'all', excludeDays: 3 }),
    { unit: 'count', value: 5, scope: 'all', days: 3 });
  assert.deepEqual(recordSettings(null), { unit: 'count', value: 0, scope: 'same_source', days: 0 });
});

// The tag the server puts on every drawn list is the one the list page filters by.
test('the draw tag matches the server', () => {
  const go = readFileSync(new URL('../../models/axm_draws.go', import.meta.url), 'utf8');
  const tag = /DrawListTag\s*=\s*"([^"]+)"/.exec(go)[1];
  assert.equal(tag, DRAW_TAG);
});
