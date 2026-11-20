// Run with several TZ values: TZ=Asia/Seoul / America/New_York / UTC node --test tests/schedule-time.test.mjs
import test from 'node:test';
import assert from 'node:assert/strict';
import {
  KST, TZ_ATTRIB, allZones, browserDiffers, customZone, browserZoneName, buildIndex, instantToWall, mergeAttribs, offsetLabel, otherZoneLines, searchIndex,
  splitAttribs, validZone, wallToInstant, zoneAbbr, zoneInfo, zoneParts,
} from '../src/schedule-time.js';

const local = (y, m, d, h, mi) => new Date(y, m - 1, d, h, mi);
const iso = (d) => d.toISOString();

test('a picker value of 2026-11-22 09:00 is 00:00 UTC in KST, whatever the browser zone', () => {
  assert.equal(iso(wallToInstant(local(2026, 11, 22, 9, 0), KST)), '2026-11-22T00:00:00.000Z');
});

test('the same picker value in New York (EST, UTC-5) and Los Angeles', () => {
  assert.equal(iso(wallToInstant(local(2026, 11, 21, 9, 0), 'America/New_York')), '2026-11-21T14:00:00.000Z');
  assert.equal(iso(wallToInstant(local(2026, 11, 21, 9, 0), 'America/Los_Angeles')), '2026-11-21T17:00:00.000Z');
});

test('daylight saving time follows the zone: 09:00 in New York in July is UTC-4', () => {
  assert.equal(iso(wallToInstant(local(2026, 7, 1, 9, 0), 'America/New_York')), '2026-07-01T13:00:00.000Z');
});

test('instant -> picker -> instant is the same moment, across days, months and the year end', () => {
  const zones = [KST, 'America/New_York', 'Europe/London', 'Asia/Kolkata', 'Pacific/Auckland', 'UTC'];
  const instants = ['2026-11-22T00:00:00Z', '2026-12-31T23:59:00Z', '2027-01-01T00:00:00Z', '2026-02-28T15:30:00Z', '2026-07-01T00:00:00Z'];
  zones.forEach((tz) => instants.forEach((s) => {
    const t = new Date(s);
    // A wall time that does not exist in THIS computer's zone cannot be a picker Date; those are skipped.
    const wall = instantToWall(t, tz);
    const wantWall = zoneParts(t, tz);
    const gotWall = zoneParts(new Date(Date.UTC(wall.getFullYear(), wall.getMonth(), wall.getDate(), wall.getHours(), wall.getMinutes())), 'UTC');
    assert.equal(gotWall.hm, wantWall.hm, `${tz} ${s}`);
    if (wall.getHours() === wantWall.hm.slice(0, 2) * 1) {
      assert.equal(iso(wallToInstant(wall, tz)), iso(t), `${tz} ${s}`);
    }
  }));
});

// A picker Date for a clock time, or null when this computer's own zone cannot show that time
// (its clocks skip it), so the case is skipped there instead of testing something else.
const exact = (y, m, d, h, mi) => {
  const t = new Date(y, m - 1, d, h, mi);
  return t.getHours() === h && t.getMinutes() === mi && t.getDate() === d ? t : null;
};

test('a clock time that happens twice (clocks go back) is the first of the two', () => {
  // New York 2030-11-03: 01:30 happens as EDT (05:30Z) and again as EST (06:30Z).
  const wall = exact(2030, 11, 3, 1, 30);
  if (wall) assert.equal(iso(wallToInstant(wall, 'America/New_York')), '2030-11-03T05:30:00.000Z');
  // London 2030-10-27: 01:30 is BST (00:30Z) and again GMT (01:30Z).
  const lon = exact(2030, 10, 27, 1, 30);
  if (lon) assert.equal(iso(wallToInstant(lon, 'Europe/London')), '2030-10-27T00:30:00.000Z');
  // Auckland 2030-04-07: 02:30 is NZDT (13:30Z the day before) and again NZST (14:30Z).
  const auk = exact(2030, 4, 7, 2, 30);
  if (auk) assert.equal(iso(wallToInstant(auk, 'Pacific/Auckland')), '2030-04-06T13:30:00.000Z');
});

test('a clock time that does not exist (clocks go forward) moves one hour later', () => {
  // New York 2030-03-10: 02:30 does not exist; 03:30 EDT is 07:30Z.
  const wall = exact(2030, 3, 10, 2, 30);
  if (wall) assert.equal(iso(wallToInstant(wall, 'America/New_York')), '2030-03-10T07:30:00.000Z');
  // London 2030-03-31: 01:30 does not exist; 02:30 BST is 01:30Z.
  const lon = exact(2030, 3, 31, 1, 30);
  if (lon) assert.equal(iso(wallToInstant(lon, 'Europe/London')), '2030-03-31T01:30:00.000Z');
  // The times around it are plain.
  const before = exact(2030, 3, 10, 1, 30);
  if (before) assert.equal(iso(wallToInstant(before, 'America/New_York')), '2030-03-10T06:30:00.000Z');
});

test('zone parts: 2026-11-22 09:00 KST in New York, Los Angeles, Tokyo; and in summer', () => {
  const t = new Date('2026-11-22T00:00:00Z');
  assert.deepEqual(zoneParts(t, 'America/New_York'), { ymd: '2026-11-21', md: '11/21', wd: 6, hm: '19:00' });
  assert.equal(zoneParts(t, 'America/Los_Angeles').hm, '16:00');
  assert.deepEqual(zoneParts(t, 'Asia/Tokyo'), { ymd: '2026-11-22', md: '11/22', wd: 0, hm: '09:00' });
  const s = new Date('2026-07-01T00:00:00Z');
  assert.deepEqual(zoneParts(s, 'America/New_York'), { ymd: '2026-06-30', md: '06/30', wd: 2, hm: '20:00' });
});

test('abbreviations: KST and JST are fixed, EST/PST come from the browser, the rest are UTC offsets', () => {
  const t = new Date('2026-11-22T00:00:00Z');
  assert.equal(zoneAbbr(t, KST), 'KST');
  assert.equal(zoneAbbr(t, 'Asia/Tokyo'), 'JST');
  assert.equal(zoneAbbr(t, 'America/New_York'), 'EST');
  assert.equal(zoneAbbr(t, 'America/Los_Angeles'), 'PST');
  assert.equal(zoneAbbr(new Date('2026-07-01T00:00:00Z'), 'America/New_York'), 'EDT');
  assert.equal(zoneAbbr(t, 'Pacific/Auckland'), 'UTC+13');
  assert.equal(zoneAbbr(t, 'Asia/Kolkata'), 'UTC+5:30');
  assert.equal(offsetLabel(t, 'America/St_Johns'), 'UTC−3:30');
});

test('zone info has the Korean name and city', () => {
  const info = zoneInfo('America/New_York', new Date('2026-11-22T00:00:00Z'));
  assert.equal(info.name, '미 동부 시간');
  assert.equal(info.city, '뉴욕');
  assert.equal(info.offset, 'UTC−5');
  assert.equal(info.parts.hm, '19:00');
});

test('validZone', () => {
  assert.equal(validZone('Asia/Seoul'), true);
  assert.equal(validZone('UTC'), true);
  assert.equal(validZone('Mars/Phobos'), false);
  assert.equal(validZone(''), false);
  assert.equal(validZone(undefined), false);
  assert.equal(validZone('Asia/Seoul\n'), false);
});

test('every listed zone is valid and the frequent ones are there', () => {
  const zones = allZones();
  assert.ok(zones.length > 300);
  ['Asia/Seoul', 'America/New_York', 'America/Los_Angeles', 'Asia/Tokyo', 'Europe/London', 'UTC'].forEach((z) => assert.ok(zones.includes(z), z));
});

test('search finds Korean names, cities, IANA names and abbreviations', () => {
  const t = new Date('2026-11-22T00:00:00Z');
  const index = buildIndex(allZones(), t);
  const searchZones = (q) => searchIndex(q, index).map((e) => e.tz);
  assert.equal(searchZones('뉴욕')[0], 'America/New_York');
  assert.equal(searchZones('london')[0], 'Europe/London');
  assert.equal(searchZones('런던')[0], 'Europe/London');
  assert.equal(searchZones('Singapore')[0], 'Asia/Singapore');
  assert.ok(searchZones('york').includes('America/New_York'));
  assert.equal(searchZones('est').includes('America/New_York'), true);
  assert.ok(searchZones('뉴').includes('America/St_Johns'));
  assert.deepEqual(searchZones(''), []);
  assert.deepEqual(searchZones('zzzzqq'), []);
});

test('the line under the input has KST first and leaves the chosen zone out', () => {
  const t = new Date('2026-11-21T14:00:00Z');
  const lines = otherZoneLines(t, 'America/New_York');
  assert.deepEqual(lines.map((l) => l.tz), [KST, 'America/Los_Angeles', 'Asia/Tokyo', 'Europe/London']);
  assert.equal(lines[0].kst, true);
  assert.equal(lines[0].parts.hm, '23:00');
  assert.equal(lines[0].parts.md, '11/21');
  assert.equal(otherZoneLines(t, KST).some((l) => l.tz === KST), false);
});

test('the zone lives in attribs under one key and is stored only for a scheduled campaign', () => {
  assert.equal(TZ_ATTRIB, 'axm_send_tz');
  assert.deepEqual(splitAttribs({ a: 1, axm_send_tz: 'America/New_York' }), { tz: 'America/New_York', stored: true, rest: { a: 1 } });
  assert.deepEqual(splitAttribs({}), { tz: KST, stored: false, rest: {} });
  assert.deepEqual(splitAttribs(null), { tz: KST, stored: false, rest: {} });
  // The API response has the keys camelCased: the key is found, and removed, in that form too.
  assert.deepEqual(splitAttribs({ b: 2, axmSendTz: 'Europe/London' }), { tz: 'Europe/London', stored: true, rest: { b: 2 } });
  assert.deepEqual(mergeAttribs({ b: 2, axmSendTz: 'Europe/London' }, KST, true), { b: 2, axm_send_tz: KST });
  assert.deepEqual(mergeAttribs({ axmSendTz: 'Europe/London' }, KST, false), {});
  assert.equal(splitAttribs({ axm_send_tz: 'Mars/Phobos' }).tz, KST);
  assert.deepEqual(mergeAttribs({ a: 1 }, 'America/New_York', true), { a: 1, axm_send_tz: 'America/New_York' });
  assert.deepEqual(mergeAttribs({ a: 1, axm_send_tz: 'Asia/Tokyo' }, 'America/New_York', false), { a: 1 });
  assert.deepEqual(mergeAttribs(null, 'Mars/Phobos', true), {});
  // A key typed into the JSON tab is replaced by the chosen zone.
  assert.deepEqual(mergeAttribs({ axm_send_tz: 'Asia/Tokyo' }, KST, true), { axm_send_tz: KST });
});

test('the browser zone note shows only when this computer differs from the chosen zone', () => {
  const t = new Date('2026-11-22T00:00:00Z');
  const own = browserZoneName();
  if (!validZone(own)) return; // an empty TZ gives "Etc/Unknown"; no browser reports that
  // This computer's own zone never differs from itself; Korea differs from a New York or UTC computer.
  assert.equal(browserDiffers(t, own), false);
  const expected = { 'Asia/Seoul': false, 'America/New_York': true, UTC: true }[own];
  if (expected !== undefined) assert.equal(browserDiffers(t, KST), expected);
});

test('a zone typed by hand is found even without a list of zones', () => {
  assert.equal(customZone('Europe/Paris'), 'Europe/Paris');
  assert.equal(customZone('  europe/paris '), 'Europe/Paris');
  assert.equal(customZone('UTC'), 'UTC');
  assert.equal(customZone('america/new_york'), 'America/New_York');
  assert.equal(customZone('Mars/Phobos'), '');
  assert.equal(customZone('paris'), '');
  assert.equal(customZone(''), '');
  assert.equal(customZone('+09:00'), '');
});
