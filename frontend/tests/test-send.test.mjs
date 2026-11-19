import test from 'node:test';
import assert from 'node:assert/strict';
import {
  SAMPLE_CUSTOM, SAMPLE_DEFAULT, SAMPLE_MAX_ATTRIBS, SAMPLE_MAX_NAME, SAMPLE_SUBSCRIBER,
  addressKind, attribsTemplate, newSample, rejectedOf, sampleLabel, samplePayload,
} from '../src/test-send.js';

test('an address is the own one, in a test domain, or left for the server to check', () => {
  const own = 'Me@Corp.example';
  const domains = ['corp.example', 'Other.Example'];
  assert.equal(addressKind(' me@corp.example ', own, domains), 'own');
  assert.equal(addressKind('ME@CORP.EXAMPLE', own, domains), 'own');
  assert.equal(addressKind('design@corp.example', own, domains), 'internal');
  assert.equal(addressKind('design@OTHER.example', own, domains), 'internal');
  // Exact domains only.
  assert.equal(addressKind('design@a.corp.example', own, domains), 'check');
  assert.equal(addressKind('design@notcorp.example', own, domains), 'check');
  assert.equal(addressKind('kim@partner.example', own, domains), 'check');
  // The own address outside every test domain, and no test domains at all.
  assert.equal(addressKind('me@home.example', 'me@home.example', domains), 'own');
  assert.equal(addressKind('design@corp.example', own, []), 'check');
  assert.equal(addressKind('design@corp.example', own, undefined), 'check');
  // No own address (an API user): an empty value never matches.
  assert.equal(addressKind('', '', domains), 'check');
  assert.equal(addressKind('x@partner.example', null, domains), 'check');
});

test('the default sample sends no sample', () => {
  assert.deepEqual(samplePayload(newSample()), { payload: null });
  assert.deepEqual(samplePayload(null), { payload: null });
  assert.deepEqual(sampleLabel(newSample()), { kind: SAMPLE_DEFAULT, name: '' });
});

test('a borrowed subscriber sends only its ID', () => {
  const sample = { ...newSample(), mode: SAMPLE_SUBSCRIBER, subscriber: { id: 7, name: '김하늘', email: 'kim@partner.example', attribs: { company: '하늘상사' } } };
  assert.deepEqual(samplePayload(sample), { payload: { subscriber_id: 7 } });
  assert.deepEqual(sampleLabel(sample), { kind: SAMPLE_SUBSCRIBER, name: '김하늘' });
  // Nobody picked yet.
  assert.deepEqual(samplePayload({ ...newSample(), mode: SAMPLE_SUBSCRIBER }), { error: 'campaigns.testSend.pickSubscriber' });
  assert.deepEqual(sampleLabel({ ...newSample(), mode: SAMPLE_SUBSCRIBER }), { kind: SAMPLE_DEFAULT, name: '' });
  // A subscriber without a name is called by the address.
  assert.equal(sampleLabel({ ...sample, subscriber: { id: 7, name: '', email: 'kim@partner.example' } }).name, 'kim@partner.example');
});

test('typed-in values are checked the way the server checks them', () => {
  const custom = (name, attribs) => ({ ...newSample(), mode: SAMPLE_CUSTOM, name, attribs });
  assert.deepEqual(samplePayload(custom(' 홍길동 ', '{"company": "크레바스", "title": ""}')),
    { payload: { name: '홍길동', attribs: { company: '크레바스' } } });
  assert.deepEqual(samplePayload(custom('홍길동', '')), { payload: { name: '홍길동', attribs: {} } });
  assert.deepEqual(samplePayload(custom('', '{"n": 0, "ok": false}')), { payload: { name: '', attribs: { n: 0, ok: false } } });
  // Nothing typed in, or only the empty template, is the default sample.
  assert.deepEqual(samplePayload(custom('', '')), { payload: null });
  assert.deepEqual(samplePayload(custom(' ', attribsTemplate([{ key: 'company', kind: 'text' }]))), { payload: null });
  assert.deepEqual(sampleLabel(custom('', '{}')), { kind: SAMPLE_DEFAULT, name: '' });
  assert.deepEqual(sampleLabel(custom('홍길동', '{}')), { kind: SAMPLE_CUSTOM, name: '홍길동' });
  assert.deepEqual(sampleLabel(custom('', '{"company":"크레바스"}')), { kind: SAMPLE_CUSTOM, name: '' });

  for (const bad of ['{company}', '["a"]', '"a"', '12', 'null']) {
    assert.deepEqual(samplePayload(custom('홍길동', bad)), { error: 'campaigns.testSend.attribsInvalid' }, bad);
  }
  // While the values are wrong the header does not claim a typed-in sample.
  assert.deepEqual(sampleLabel(custom('홍길동', '{company}')), { kind: SAMPLE_DEFAULT, name: '' });
  assert.deepEqual(samplePayload(custom('가'.repeat(SAMPLE_MAX_NAME + 1), '')), { error: 'campaigns.testSend.nameTooLong' });
  assert.equal(samplePayload(custom('가'.repeat(SAMPLE_MAX_NAME), '')).payload.name.length, SAMPLE_MAX_NAME);
  // The size limit is in bytes, as on the server.
  const long = JSON.stringify({ a: '가'.repeat(SAMPLE_MAX_ATTRIBS / 3) });
  assert.deepEqual(samplePayload(custom('', long)), { error: 'campaigns.testSend.attribsTooLong' });
});

test('the attribute template holds the standard text keys', () => {
  const tpl = attribsTemplate([{ key: 'country', kind: 'country' }, { key: 'follower', kind: 'integer' },
    { key: 'company', kind: 'text' }, { key: 'title', kind: 'text' }]);
  assert.deepEqual(JSON.parse(tpl), { company: '', title: '' });
  assert.equal(attribsTemplate(undefined), '{}');
});

test('the refused addresses are read from the error of a test request', () => {
  const err = { response: { data: { message: 'x', data: { rejected: ['A@outside.example', 'b@outside.example'] } } } };
  assert.deepEqual(rejectedOf(err), ['a@outside.example', 'b@outside.example']);
  assert.deepEqual(rejectedOf({ response: { data: { message: 'x' } } }), []);
  assert.deepEqual(rejectedOf({ response: { data: { data: { rejected: 'x' } } } }), []);
  assert.deepEqual(rejectedOf(new Error('network')), []);
  assert.deepEqual(rejectedOf(undefined), []);
});
