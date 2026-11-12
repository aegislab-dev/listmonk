import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import {
  convertValue, countryCode, countryHint, normalizeName, standardByKey, standardFor, wholeNumber, withStandardFirst,
} from '../src/lib/standard-attribs.js';
import { COUNTRIES, COUNTRY_ALIASES } from '../src/lib/countries.js';

const STANDARD = JSON.parse(readFileSync(new URL('../../internal/core/axm_standard_attribs.json', import.meta.url), 'utf8'));

test('the dictionary has the five standard items and finds them by key and alias', () => {
  assert.deepEqual(STANDARD.map(({ key, kind }) => [key, kind]), [
    ['country', 'country'], ['follower', 'integer'], ['company', 'text'], ['title', 'text'], ['industry', 'text'],
  ]);
  assert.equal(normalizeName('  Follower_Count '), 'followercount');
  assert.equal(normalizeName('팔로워 수'), normalizeName('팔로워수'));
  assert.equal(standardFor(STANDARD, 'Job-Title').key, 'title');
  assert.equal(standardFor(STANDARD, 'COUNTRY').key, 'country');
  assert.equal(standardFor(STANDARD, '소재지'), null);
  assert.equal(standardFor(STANDARD, ''), null);
  assert.equal(standardFor(undefined, 'country'), null);
  assert.equal(standardByKey(STANDARD, 'follower').kind, 'integer');
  assert.equal(standardByKey(STANDARD, 'followers'), null);
  assert.deepEqual(withStandardFirst(STANDARD, ['gmv', 'follower', 'gmv']), ['country', 'follower', 'company', 'title', 'industry', 'gmv']);
  // No name answers to two items.
  const seen = new Map();
  STANDARD.forEach(({ key, aliases }) => [key, ...aliases].forEach((name) => {
    const n = normalizeName(name);
    assert.ok(!seen.has(n) || seen.get(n) === key, name);
    seen.set(n, key);
  }));
});

test('country table: 249 codes, and no alias or name points at two countries', () => {
  assert.equal(COUNTRIES.length, 249);
  assert.equal(new Set(COUNTRIES.map(([code]) => code)).size, 249);
  COUNTRIES.forEach(([code, ...names]) => {
    assert.match(code, /^[A-Z]{2}$/);
    assert.ok(names.length >= 2, code);
    names.forEach((name) => assert.equal(countryCode(name), code, name));
  });
  Object.entries(COUNTRY_ALIASES).forEach(([code, names]) => {
    assert.ok(COUNTRIES.some(([c]) => c === code), code);
    names.forEach((name) => assert.equal(countryCode(name), code, name));
  });
});

test('country values: codes in any case, names in English and Korean, common abbreviations', () => {
  const read = {
    US: ['US', 'us', 'Us', 'USA', 'usa', 'U.S.', 'U.S.A.', 'United States', 'United states', 'United States of America', '미국', 'Estados Unidos', 'EE.UU', 'EE. UU.', 'EEUU', ' US '],
    GB: ['GB', 'UK', 'uk', 'United Kingdom', '영국', 'England'],
    KR: ['KR', 'kr', 'Korea', 'South Korea', 'South korea', 'Republic of Korea', '대한민국', '한국'],
    PL: ['PL', 'Poland', 'POLAND', 'Polska', 'polska', 'Poland (Polska)'],
    AE: ['AE', 'UAE', 'Uae', 'United Arab Emirates', '아랍에미리트'],
    CA: ['CA', 'Canada', 'Canadá'],
    DE: ['DE', 'Germany', 'Deutschland', 'Niemcy', '독일'],
    PH: ['PH', 'Philippines', '필리핀'],
    CI: ['Côte d’Ivoire', "Cote d'Ivoire", 'Ivory Coast'],
    HK: ['Hong Kong', '홍콩'],
    TR: ['Türkiye', 'Turkey', '터키', '튀르키예'],
  };
  Object.entries(read).forEach(([code, texts]) => texts.forEach((text) => assert.equal(countryCode(text), code, text)));
  // Cities, states, two countries, unions, Korean regions and unknown codes are not a country.
  for (const text of ['Dubai', 'UAE, Dubai', 'Florida', 'Miami, FL', 'Philippines/ UAE', 'EU', 'KO', 'XX', '서울', '경기', '부산', 'Poland (Germany)', 'USA1', '']) {
    assert.equal(countryCode(text), '', text);
  }
  assert.deepEqual(convertValue('country', 'US'), { status: 'kept', value: 'US' });
  assert.deepEqual(convertValue('country', ' us '), { status: 'changed', value: 'US' });
  assert.deepEqual(convertValue('country', '미국'), { status: 'changed', value: 'US' });
  assert.deepEqual(convertValue('country', '서울'), { status: 'unreadable', value: '서울' });
  for (const text of ['', ' ', '-', '—', '?', 'N/A', 'n/a', 'Unknown', 'none', 'null', '없음', '모름', '미상', '해당 없음']) {
    assert.equal(convertValue('country', text).status, 'blank', text);
    assert.equal(convertValue('integer', text).status, 'blank', text);
  }
});

test('follower values: commas, K/M/B and Korean units become whole numbers', () => {
  const read = [
    ['0', 0], ['14900', 14900], ['000123', 123], ['14,900', 14900], ['1,234,567', 1234567], ['14900.0', 14900], ['1,234.00', 1234],
    ['10K', 10000], ['10k', 10000], ['10 K', 10000], ['1.2M', 1200000], ['1.2m', 1200000], ['2.5B', 2500000000], ['1,200K', 1200000],
    ['1.2345K', 1235], ['0.5K', 500], ['0.0004K', 0],
    ['1만', 10000], ['1.2만', 12000], ['3.5천', 3500], ['1만 2천', 12000], ['1만2천', 12000], ['12만3,400', 123400], ['2천만', 20000000],
    ['5백만', 5000000], ['1억', 100000000], ['1억 2천만', 120000000], ['3천5백', 3500], ['1.2만명', 12000], ['1,500명', 1500],
    ['999999999999999', 999999999999999],
  ];
  read.forEach(([text, number]) => assert.equal(wholeNumber(text), number, text));
  // Not a count: approximations, ranges, signs, bad grouping, a bare fraction (12.500 may mean 12,500), too large.
  for (const text of ['', '약 1만', '10K+', '<10K', '10K-100K', '-5', '+5', '1,2000', '12,34', '14900.5', '12.500', '0.4', '1.234.567', '1e5',
    '천', '만', '1만만', '1억2억', '10KK', '만 명', 'abc', '12abc', '１２３', '9999999999999999', '99999999억']) {
    assert.equal(wholeNumber(text), null, text);
  }
  assert.deepEqual(convertValue('integer', '14900'), { status: 'kept', value: 14900 });
  assert.deepEqual(convertValue('integer', '10K'), { status: 'changed', value: 10000 });
  assert.deepEqual(convertValue('integer', '약 1만'), { status: 'unreadable', value: '약 1만' });
  // Text items keep what is written, trimmed.
  assert.deepEqual(convertValue('text', ' (주)예시 '), { status: 'kept', value: '(주)예시' });
  assert.deepEqual(convertValue('text', '-'), { status: 'kept', value: '-' });
});

test('a condition on country hints at the code for a typed name', () => {
  assert.equal(countryHint(STANDARD, 'country', 'US'), null);
  assert.equal(countryHint(STANDARD, 'country', ''), null);
  assert.equal(countryHint(STANDARD, 'follower', '미국'), null);
  assert.equal(countryHint(STANDARD, 'region', '미국'), null);
  assert.deepEqual(countryHint(STANDARD, 'country', '미국'), { code: 'US' });
  assert.deepEqual(countryHint(STANDARD, ' country ', 'us'), { code: 'US' });
  assert.deepEqual(countryHint(STANDARD, 'country', '서울'), { code: '' });
});
