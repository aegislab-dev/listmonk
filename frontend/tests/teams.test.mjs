import test from 'node:test';
import assert from 'node:assert/strict';
import {
  NO_TEAM, defaultTeamTag, isTeamTag, plainTags, tagsForSave, teamName, teamTagOf, teamTags,
} from '../src/teams.js';

test('a team tag starts with the team prefix and has a name', () => {
  assert.equal(isTeamTag('팀:마케팅2팀'), true);
  assert.equal(isTeamTag('팀:'), false);
  assert.equal(isTeamTag('마케팅'), false);
  assert.equal(isTeamTag(undefined), false);
  assert.deepEqual(teamTags(['북미', '팀:가', '팀:나', '뽑은-리스트']), ['팀:가', '팀:나']);
  assert.deepEqual(plainTags(['북미', '팀:가', '뽑은-리스트']), ['북미', '뽑은-리스트']);
  assert.deepEqual(teamTags(null), []);
  assert.equal(teamName('팀:마케팅2팀'), '마케팅2팀');
});

test('a team name makes the tag the server stores: whitespace becomes a dash', () => {
  assert.equal(teamTagOf('마케팅2팀'), '팀:마케팅2팀');
  assert.equal(teamTagOf('  마케팅   2팀 '), '팀:마케팅-2팀');
});

test('saving a list keeps, replaces or drops its team', () => {
  const current = ['x', '팀:가', '팀:나'];
  // Nothing chosen: the teams stay as they are.
  assert.deepEqual(tagsForSave(['x', 'y'], current, ''), { tags: ['x', 'y', '팀:가', '팀:나'], noTeam: false });
  // One team chosen: it replaces the others.
  assert.deepEqual(tagsForSave(['x'], current, '팀:다'), { tags: ['x', '팀:다'], noTeam: false });
  // No team on purpose.
  assert.deepEqual(tagsForSave(['x', '팀:가'], current, NO_TEAM), { tags: ['x'], noTeam: true });
  // A new list.
  assert.deepEqual(tagsForSave([], undefined, '팀:가'), { tags: ['팀:가'], noTeam: false });
  assert.deepEqual(tagsForSave([], undefined, ''), { tags: [], noTeam: false });
});

test('the default team is the first candidate that is a team that exists', () => {
  const known = [{ tag: '팀:가' }, { tag: '팀:나' }];
  assert.equal(defaultTeamTag(['팀:다', '팀:나'], known), '팀:나');
  assert.equal(defaultTeamTag(['팀:다'], known), '');
  assert.equal(defaultTeamTag([], known), '');
  assert.equal(defaultTeamTag(undefined, undefined), '');
});
