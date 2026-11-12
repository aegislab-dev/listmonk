import test from 'node:test';
import assert from 'node:assert/strict';
import { BASE_ITEMS, goString, buildToken, parseTokens, collectAttribKeys, loadItems } from '../src/personalization.js';

const attr = (key) => ({ id: `attrib:${key}`, key });
const join = (parts) => parts.map((p) => p.type === 'token' ? p.raw : p.value).join('');

test('Go strings escape punctuation and astral characters without surrogate pairs', () => {
  assert.equal(goString('한글 A1 "\\&<>\'{}\n😀'), '"한글 A1 \\u0022\\u005c\\u0026\\u003c\\u003e\\u0027\\u007b\\u007d\\u000a\\U0001f600"');
  assert.throws(() => goString('\ud800'));
});

test('references distinguish fixed fields, identifiers and literal keys', () => {
  assert.equal(buildToken(BASE_ITEMS[0]), '{{ .Subscriber.Name }}');
  assert.equal(buildToken(BASE_ITEMS[1], '고객'), '{{ .Subscriber.Email | default "고객" }}');
  for (const key of ['company_name', '회사명', '_x']) {
    assert.equal(buildToken(attr(key)), `{{ .Subscriber.Attribs.${key} }}`);
  }
  for (const key of ['회사 이름', 'a.b', 'a-b', '1st', 'a½', '😀', 'a"}']) {
    assert.equal(buildToken(attr(key)), `{{ index .Subscriber.Attribs ${goString(key)} }}`);
  }
});

test('recognized tokens retain raw bytes and decode keys/fallbacks', () => {
  for (const item of [...BASE_ITEMS, ...['한글', '공백 키', 'a.b', 'a-b', '1st', 'a½', '"}😀'].map(attr)]) {
    for (const fallback of ['', '고객', '"\\&<>{}\n😀']) {
      const raw = buildToken(item, fallback);
      const parts = parseTokens(`앞\n${raw}뒤`);
      assert.equal(join(parts), `앞\n${raw}뒤`);
      assert.equal(parts[1].type, 'token');
      assert.equal(parts[1].item.id, item.id);
      assert.equal(parts[1].fallback, fallback);
    }
  }
  const spaced = '{{\n .Subscriber.Name|default "고객"\t}}';
  assert.equal(parseTokens(spaced)[0].type, 'token');
  assert.equal(join(parseTokens(spaced)), spaced);
});

test('custom Go templates and unsupported strings are preserved', () => {
  const examples = ['', '{{ TrackLink "https://example.invalid" }}', '{{ if .Subscriber.Name }}yes{{ end }}',
    '{{ .Subscriber.Name | upper }}', '{{ .Subscriber.Attribs.a.b }}', '{{ index .Subscriber.Attribs "\\q" }}',
    '{{ .Subscriber.Name | default "\\U00110000" }}', '{{ .Subscriber.Name | default "\\ud800" }}', '{{ unfinished'];
  for (const text of examples) {
    assert.equal(join(parseTokens(text)), text);
    assert.equal(parseTokens(text).some((p) => p.type === 'token'), false);
  }
  const mixed = `${examples[1]}${buildToken(BASE_ITEMS[0])}${examples[2]}`;
  assert.equal(join(parseTokens(mixed)), mixed);
  assert.equal(parseTokens(mixed).filter((p) => p.type === 'token').length, 1);
});

test('only scalar top-level keys leave the sample collector, never values', () => {
  const keys = collectAttribKeys([{ attribs: { a: '', b: 0, c: false, obj: {}, array: ['secret'], nil: null, mix: [] } },
    { attribs: { mix: 'secret', a: 'secret' } }, {}]);
  assert.deepEqual(keys, ['a', 'b', 'c', 'mix']);
  assert.equal(JSON.stringify(keys).includes('secret'), false);
});

test('samples are bounded, deduplicated, labelled and do not query without lists', async () => {
  const requests = [];
  const api = { getSubscriberAttribSample: async (params) => {
    requests.push(params);
    return { results: [{ attribs: { price: 'secret', 회사명: 'secret' } }] };
  } };
  assert.deepEqual(await loadItems(api, []), { items: BASE_ITEMS, noLists: true, error: false, truncated: false });
  assert.equal(requests.length, 0);
  const result = await loadItems(api, [1, 1, 2, 3, 4, 5, 6], { price: '가격' });
  assert.equal(requests.length, 5);
  assert.ok(requests.every((p) => p.per_page === 50 && p.page === 1 && p.order_by === 'id' && p.order === 'desc'));
  assert.equal(result.truncated, true);
  assert.equal(result.items.find((p) => p.key === 'price').label, '가격');
  assert.equal(JSON.stringify(result).includes('secret'), false);
  const failed = await loadItems({ getSubscriberAttribSample: () => Promise.reject(new Error('403')) }, [1]);
  assert.equal(failed.error, true);
  assert.deepEqual(failed.items, BASE_ITEMS);
  // Standard attributes are offered first, sampled or not, with or without lists or access.
  const standard = ['country', 'follower'];
  const labels = { country: '국가', follower: '팔로워 수' };
  const sampled = { getSubscriberAttribSample: async () => ({ results: [{ attribs: { gmv: 1, follower: 2 } }] }) };
  assert.deepEqual((await loadItems(sampled, [1], labels, standard)).items.slice(BASE_ITEMS.length), [
    { id: 'attrib:country', key: 'country', label: '국가' }, { id: 'attrib:follower', key: 'follower', label: '팔로워 수' },
    { id: 'attrib:gmv', key: 'gmv', label: 'gmv' },
  ]);
  assert.deepEqual((await loadItems(sampled, [], labels, standard)).items.slice(BASE_ITEMS.length).map(({ key }) => key), standard);
  const denied = await loadItems({ getSubscriberAttribSample: () => Promise.reject(new Error('403')) }, [1], labels, standard);
  assert.deepEqual(denied.items.slice(BASE_ITEMS.length).map(({ key }) => key), standard);
});
