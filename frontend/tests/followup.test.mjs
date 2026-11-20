import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync, readdirSync } from 'node:fs';
import { conditionWords } from '../src/followup.js';

const read = (path) => readFileSync(new URL(path, import.meta.url), 'utf8');
const ko = JSON.parse(read('../../i18n/ko.json'));
const en = JSON.parse(read('../../i18n/en.json'));
const files = [
  '../src/components/CampaignReactionList.vue', '../src/components/CampaignFollowupBanner.vue', '../src/views/Campaign.vue',
];

// Keys the components read through f('key') or $t('campaigns.followup.key').
function usedKeys() {
  const keys = new Set();
  for (const file of files) {
    const text = read(file);
    for (const m of text.matchAll(/\bf\('([A-Za-z]+)'/g)) keys.add(`campaigns.followup.${m[1]}`);
    for (const m of text.matchAll(/\$t\('(campaigns\.followup\.[A-Za-z]+)'/g)) keys.add(m[1]);
  }
  return keys;
}

// Keys the server reads (cmd and internal/core go through i18n.T / Ts).
function serverKeys() {
  const keys = new Set();
  const walk = (dir) => {
    for (const entry of readdirSync(new URL(dir, import.meta.url), { withFileTypes: true })) {
      const path = `${dir}${entry.name}`;
      if (entry.isDirectory()) walk(`${path}/`);
      else if (/\.go$/.test(entry.name) && !/_test\.go$/.test(entry.name)) {
        const text = read(path);
        for (const m of text.matchAll(/"(campaigns\.followup\.[A-Za-z]+)"/g)) keys.add(m[1]);
        // followupBadRequest(code, "key", ...) builds the key itself.
        for (const m of text.matchAll(/followupBadRequest\([^,]+, "([A-Za-z]+)"/g)) keys.add(`campaigns.followup.${m[1]}`);
      }
    }
  };
  walk('../../cmd/');
  walk('../../internal/core/');
  return keys;
}

test('every follow-up text the screens and the server use exists in Korean and English', () => {
  const keys = new Set([...usedKeys(), ...serverKeys()]);
  assert.ok(keys.size > 20, `found only ${keys.size} keys`);
  for (const key of keys) {
    assert.ok(ko[key], `ko: ${key}`);
    assert.ok(en[key], `en: ${key}`);
  }
});

test('the two languages carry the same follow-up keys and the same placeholders', () => {
  const names = (o) => Object.keys(o).filter((k) => k.startsWith('campaigns.followup.') || k === 'audit.action.campaign.followup').sort();
  assert.deepEqual(names(ko), names(en));
  const holes = (s) => (s.match(/\{[A-Za-z]+\}/g) || []).sort().join(',');
  for (const key of names(ko)) assert.equal(holes(ko[key]), holes(en[key]), key);
});

test('the reaction list dialog keeps the hooks of the list-only flow', () => {
  const text = read('../src/components/CampaignReactionList.vue');
  for (const hook of ['reaction-list', 'reaction-check', 'reaction-create', 'reaction-name', 'reaction-done', 'reaction-view', 'reaction-campaign']) {
    assert.ok(text.includes(`data-cy="${hook}"`), hook);
  }
  // The follow-up part is optional: the request only carries it when the box is checked.
  assert.match(text, /wantsFollowup\s*\n?\s*\?\s*\{\s*followup: true/);
});

test('a follow-up made for everyone who received the campaign still names its condition', () => {
  const t = (key) => ko[key] || key;
  assert.equal(conditionWords({ open: 'any', click: 'any' }, t), ko['campaigns.reactionList.received']);
  assert.equal(conditionWords({ open: 'yes', click: 'no' }, t), `${ko['campaigns.reactionList.open.yes']} · ${ko['campaigns.reactionList.click.no']}`);
  assert.equal(conditionWords({ open: 'no', click: 'any' }, t), ko['campaigns.reactionList.open.no']);
  assert.equal(conditionWords(null, t), '');
});
