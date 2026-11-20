import test from 'node:test';
import assert from 'node:assert/strict';
import { isPlaceholderFrom, matchFromEntry } from '../src/senders.js';

test('placeholder rule matches the server, quotes included', () => {
  for (const v of ['change-me@crevasse.ai', 'Team <Change-Me@crevasse.ai>', 'Team <"change-me"@crevasse.ai>',
    'a@example.com', 'a@mail.example.org', 'a@x.invalid', '"News Team"@example.org']) {
    assert.equal(isPlaceholderFrom(v), true, v);
  }
  for (const v of ['aegislab_us@crevasse.ai', 'Team <team@crevasse.ai>', 'noreply@listmonk.yoursite.com', '', 'not an address']) {
    assert.equal(isPlaceholderFrom(v), false, v);
  }
});

test('match a From to the list: same entry, same address, quoted local part', () => {
  const list = ['aegislab_us@crevasse.ai', 'AEGIS <team@crevasse.ai>', '"two words"@crevasse.ai'];
  assert.equal(matchFromEntry('aegislab_us@crevasse.ai', list), list[0]);
  assert.equal(matchFromEntry(' AegisLab_US@Crevasse.ai ', list), list[0]);
  assert.equal(matchFromEntry('Other <team@crevasse.ai>', list), list[1]);
  assert.equal(matchFromEntry('"two words"@crevasse.ai', list), list[2]);
  assert.equal(matchFromEntry('X <"two words"@crevasse.ai>', list), list[2]);
  assert.equal(matchFromEntry('AEGIS <team@crevasse.ai> (Newsletter)', list), list[1]);
  assert.equal(matchFromEntry('team@crevasse.ai (Newsletter)', list), list[1]);
  assert.equal(matchFromEntry('"News <Korea>" <team@crevasse.ai>', list), list[1]);
  assert.equal(matchFromEntry('News <team@crevasse.ai> (Newsletter (Korea))', list), list[1]);
  assert.equal(matchFromEntry('other@crevasse.ai', list), '');
  assert.equal(matchFromEntry('', list), '');
  assert.equal(matchFromEntry('a@crevasse.ai', []), '');
});
