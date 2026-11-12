import { test } from 'node:test';
import assert from 'node:assert/strict';
import Papa from 'papaparse';
import {
  LIMITS, STORAGE_KEY, checkInputSize, cleanEmail, validEmail, suggestKey, isSpelledKey, romanize,
  parseCSV, suggestMapping, validateMapping, convertCSV, readMemory, rememberMapping,
  inspectOriginalCSV, inspectCSVHeader,
} from '../src/lib/csv-import.js';

const converted = (text, mapping, keys) => {
  const parsed = parseCSV(text);
  return convertCSV(parsed, mapping || suggestMapping(parsed), keys);
};
const unpack = (result) => Papa.parse(new TextDecoder().decode(result.bytes), { header: true }).data;

test('quoted comma, escaped quotes, newline and empty fields round trip through standard CSV and JSON', () => {
  const result = converted('메일 주소,성함,회사명,관심 태그,External ID,메모\n ONE@EXAMPLE.INVALID ,"가상 ""한"",이름","첫 줄\n둘째 줄","개발, AI,개발, ,마케팅",00123,버림');
  const [row] = unpack(result);
  assert.equal(row.email, 'one@example.invalid');
  assert.equal(row.name, '가상 "한",이름');
  assert.deepEqual(JSON.parse(row.attributes), { company_name: '첫 줄\n둘째 줄', tags: ['개발', 'AI', '마케팅'], external_id: '00123' });
  assert.equal(result.summary.cleaned, 1);
  assert.equal(new TextDecoder().decode(result.bytes).split('\r\n')[0], 'email,name,attributes');
});

for (const delimiter of [';', '\t', '|']) {
  test(`detect ${JSON.stringify(delimiter)} and always output comma CSV`, () => {
    const parsed = parseCSV(`email${delimiter}name\na@example.invalid${delimiter}가상`);
    assert.equal(parsed.delimiter, delimiter);
    assert.equal(unpack(convertCSV(parsed, suggestMapping(parsed)))[0].name, '가상');
  });
}

test('duplicate and blank header labels use separate column indexes', () => {
  const parsed = parseCSV('email,회사,회사,\na@example.invalid,A,B,C');
  const mapping = [{ role: 'email' }, { role: 'attribute', key: 'first' }, { role: 'attribute', key: 'second' }, { role: 'discard' }];
  assert.deepEqual(parsed.columns, ['email', '회사', '회사', '']);
  assert.deepEqual(JSON.parse(unpack(convertCSV(parsed, mapping))[0].attributes), { first: 'A', second: 'B' });
});

test('suggestions use memory, known labels, email samples, then a name made from the label', () => {
  const parsed = parseCSV('회사명,성함,주소,Customer ID,새로운열,비고,관심사\n가상,가상,a@example.invalid,00123,문자열,버림,AI');
  assert.deepEqual(suggestMapping(parsed, { columns: { 회사명: { role: 'attribute', key: 'legacy' } } }), [
    { role: 'attribute', key: 'legacy' }, { role: 'name', key: '' }, { role: 'email', key: '' },
    { role: 'attribute', key: 'customer_id' }, { role: 'attribute', key: 'saerounyeol' }, { role: 'discard', key: '' }, { role: 'tags', key: 'tags' },
  ]);
});

test('every email dictionary header suggests email without relying on sample values', () => {
  const headers = ['이메일', '메일', '메일 주소', 'e-mail', 'email'];
  const parsed = parseCSV(`${headers.join(',')}\n${headers.map(() => 'not an address').join(',')}`);
  assert.deepEqual(suggestMapping(parsed), headers.map(() => ({ role: 'email', key: '' })));
});

test('attribute names are made from labels: known terms, spelled-out Hangul and tidied Latin', () => {
  assert.equal(suggestKey('고객등급'), 'customer_grade');
  assert.equal(suggestKey(' 고객 등급 '), 'customer_grade');
  assert.equal(suggestKey('휴대폰 번호'), 'phone');
  assert.equal(romanize('가입 경로'), 'gaip gyeongro');
  assert.equal(suggestKey('가입 경로'), 'gaip_gyeongro');
  assert.equal(suggestKey('값없음'), 'gapeopeum');
  assert.equal(suggestKey('Customer Grade'), 'customer_grade');
  assert.equal(suggestKey('externalID-2'), 'external_id_2');
  assert.equal(suggestKey('2024 매출'), 'item_2024_maechul');
  assert.equal(suggestKey('###'), '');
  assert.equal(suggestKey('customer_grade'), 'customer_grade');
  assert.equal(isSpelledKey('가입 경로', 'gaip_gyeongro'), true);
  assert.equal(isSpelledKey('고객등급', 'customer_grade'), false);
  assert.equal(isSpelledKey('가입 경로', 'signup_path'), false);
  assert.equal(isSpelledKey('Signup Path', 'signup_path'), false);
  // Every suggestion for a real label is a valid new key.
  const parsed = parseCSV('이메일,가입 경로,관심 분야,고객 등급,관심 태그,비고,Signup Path\na@example.invalid,박람회,AI,VIP,"AI, 개발",메모,web');
  const mapping = suggestMapping(parsed);
  assert.deepEqual(mapping, [
    { role: 'email', key: '' }, { role: 'attribute', key: 'gaip_gyeongro' }, { role: 'attribute', key: 'gwansim_bunya' },
    { role: 'attribute', key: 'customer_grade' }, { role: 'tags', key: 'tags' }, { role: 'discard', key: '' }, { role: 'attribute', key: 'signup_path' },
  ]);
  assert.deepEqual(validateMapping(mapping), []);
  // A label that yields no name falls back to its position.
  assert.deepEqual(suggestMapping(parseCSV('email,,###\na@example.invalid,x,y')).map(({ key }) => key), ['', 'column_2', 'column_3']);
});

test('header toggle keeps first data row; standard detection only accepts standard names', () => {
  const text = 'a@example.invalid,A\nb@example.invalid,B';
  assert.equal(parseCSV(text).headerHint, true);
  const noHeader = parseCSV(text, { header: false });
  assert.deepEqual(noHeader.columns, ['Column 1', 'Column 2']);
  assert.equal(noHeader.rows[0].row, 1);
  assert.equal(noHeader.total, 2);
  assert.equal(parseCSV('email,name,attributes\na@example.invalid,A,').standard, true);
  assert.equal(parseCSV('email,company\na@example.invalid,A').standard, false);
});

test('mapping requires one email, at most one name and unique valid keys; existing keys are preserved', () => {
  assert.deepEqual(validateMapping([{ role: 'discard' }]).map((err) => err.type), ['emailColumns']);
  assert.equal(validateMapping([{ role: 'email' }, { role: 'email' }])[0].type, 'emailColumns');
  assert.equal(validateMapping([{ role: 'email' }, { role: 'name' }, { role: 'name' }])[0].type, 'nameColumns');
  const keys = ['회사명', 'company-name'];
  const mapping = [{ role: 'email' }, ...keys.map((key) => ({ role: 'attribute', key }))];
  assert.equal(validateMapping(mapping).length, 2);
  assert.deepEqual(validateMapping(mapping, keys), []);
  for (const key of ['', 'Upper', '1key', 'has space', 'hy-phen']) {
    assert.equal(validateMapping([{ role: 'email' }, { role: 'attribute', key }])[0].type, 'key');
  }
  assert.equal(validateMapping([{ role: 'email' }, { role: 'tags', key: 'tags' }, { role: 'attribute', key: 'tags' }])[0].type, 'duplicateKey');
  assert.throws(() => converted('email\na@example.invalid', []), { code: 'mapping' });
});

test('empty attributes omitted; blank names and attributes serialize to empty fields; prototype keys are safe', () => {
  const parsed = parseCSV('email,name,code,tags,legacy\na@example.invalid, ,00123," , , ",ok\nb@example.invalid,B, , ,');
  const mapping = [{ role: 'email' }, { role: 'name' }, { role: 'attribute', key: 'code' }, { role: 'tags', key: 'tags' }, { role: 'attribute', key: '__proto__' }];
  const rows = unpack(convertCSV(parsed, mapping, ['__proto__']));
  assert.deepEqual(JSON.parse(rows[0].attributes), JSON.parse('{"code":"00123","__proto__":"ok"}'));
  assert.equal(rows[0].name, '');
  assert.equal(rows[1].attributes, '');
});

test('email cleaning, validation, first duplicate wins, logical source rows and issue examples', () => {
  assert.equal(cleanEmail('\ufeff\u00a0< AB\u200b@EXAMPLE.INVALID >\u2060'), 'ab@example.invalid');
  for (const email of ['a@localhost', 'a@@example.invalid', '@example.invalid', 'a@.invalid', 'a@example.', 'a b@example.invalid', 'a\x01@example.invalid']) assert.equal(validEmail(email), false, email);
  const parsed = parseCSV('email,name\n AB@EXAMPLE.INVALID ,"first\nname"\nab@example.invalid,second\n,b\nbad@localhost,b\n\nc@example.invalid,C');
  const result = convertCSV(parsed, suggestMapping(parsed));
  assert.deepEqual(result.summary, { imported: 2, cleaned: 1, empty: 1, invalid: 1, duplicate: 1, malformed: 0 });
  assert.deepEqual(result.sourceRows, [2, 7]);
  assert.equal(unpack(result)[0].name, 'first\nname');
  assert.deepEqual(result.examples.duplicate, [{ row: 3, email: 'ab@example.invalid' }]);
  assert.deepEqual(result.examples.invalid, [{ row: 5, email: 'bad@localhost' }]);
  assert.deepEqual(result.preview[0], { row: 2, email: 'ab@example.invalid', name: 'first\nname', attributes: [] });
});

test('malformed widths are excluded and keep source row numbers', () => {
  const parsed = parseCSV('email,name\na@example.invalid,A\nb@example.invalid,B,extra');
  assert.deepEqual(parsed.issues, [{ row: 3, type: 'width' }]);
  assert.deepEqual(convertCSV(parsed, suggestMapping(parsed)).sourceRows, [2]);
});

test('unclosed or invalid closing quotes reject the whole file instead of hiding later people', () => {
  const people = Array.from({ length: 10 }, (_, i) => `person-${i}@example.invalid,Name ${i}`);
  for (const bad of ['person-2@example.invalid,"unclosed', 'person-2@example.invalid,"홍"길동']) {
    const rows = [...people];
    rows[2] = bad;
    assert.throws(() => parseCSV(`email,name\n${rows.join('\n')}`), { code: 'quotes' });
  }
  assert.throws(() => parseCSV('email,"name\na@example.invalid,A'), { code: 'quotes' });
  assert.throws(() => parseCSV('email,name\na@example.invalid,"unclosed'), { code: 'quotes' });
  // Legitimate multiline cells must still produce all people and logical rows.
  const parsed = parseCSV('email,name\na@example.invalid,"first\nsecond"\nb@example.invalid,"He said ""yes"""');
  assert.equal(parsed.total, 2);
  assert.deepEqual(convertCSV(parsed, suggestMapping(parsed)).sourceRows, [2, 3]);
});

test('mapping row limits do not prevent inspecting a standard original CSV header', () => {
  const text = `email,name,attributes\n${'a@example.invalid,A,\n'.repeat(60000)}`;
  assert.deepEqual(inspectCSVHeader(text), { delimiter: ',', standard: true });
  assert.throws(() => parseCSV(text), { code: 'rowLimit', total: 60000 });
});

test('original inspection has bounded reads and no mapping size limit, but validates all UTF-8 bytes', async () => {
  const header = 'email,name,attributes\n';
  const text = header + 'a'.repeat(512 * 1024 - header.length - 1) + '똠' + 'a'.repeat(LIMITS.input);
  const blob = new Blob([text]);
  const reads = [];
  const file = { size: blob.size, slice(start, end) { reads.push(end - start); return blob.slice(start, end); } };
  assert.deepEqual(await inspectOriginalCSV(file), { encoding: 'utf-8', delimiter: ',', standard: true });
  assert.ok(reads.length > 20 && reads.every((size) => size <= 512 * 1024));
  await assert.rejects(inspectOriginalCSV(new Blob([text, new Uint8Array([0xe3, 0x81])])), { code: 'rawEncoding' });
  await assert.rejects(inspectOriginalCSV(new Blob([header, new Uint8Array([0x8c, 0x63])])), { code: 'rawEncoding' });
  await assert.rejects(inspectOriginalCSV(new Blob([text, '\0'])), { code: 'nul' });
});

test('input, data row and output limits stop processing; NUL and empty files rejected', () => {
  assert.doesNotThrow(() => checkInputSize(LIMITS.input));
  assert.throws(() => checkInputSize(LIMITS.input + 1), { code: 'inputLimit' });
  assert.throws(() => parseCSV(`email\n${'a@example.invalid\n'.repeat(50001)}`), { code: 'rowLimit' });
  assert.throws(() => converted(`email,name\na@example.invalid,${'가'.repeat(Math.ceil(LIMITS.output / 3))}`), { code: 'outputLimit' });
  assert.throws(() => parseCSV('email\na\0@example.invalid'), { code: 'nul' });
  assert.throws(() => parseCSV(''), { code: 'empty' });
});

test('optional storage persists only column labels and mappings, sanitizes and caps entries', () => {
  let saved;
  const storage = { getItem: () => saved, setItem: (key, value) => { assert.equal(key, STORAGE_KEY); saved = value; } };
  const parsed = parseCSV('이메일,이름,회사\nprivate@example.invalid,가상 이름,비밀 값');
  rememberMapping(storage, parsed.columns, suggestMapping(parsed));
  assert.equal(saved.includes('private@example.invalid'), false);
  assert.equal(saved.includes('가상 이름'), false);
  assert.equal(saved.includes('비밀 값'), false);
  assert.deepEqual(readMemory(storage).keys, ['company_name']);
  const large = Array.from({ length: 201 }, (_, i) => `key_${i}`);
  rememberMapping(storage, large, large.map((key) => ({ role: 'attribute', key })));
  assert.equal(Object.keys(readMemory(storage).columns).length, 200);
  assert.equal(readMemory(storage).keys.length, 200);
  assert.doesNotThrow(() => rememberMapping({ getItem() { throw Error(); }, setItem() { throw Error(); } }, parsed.columns, suggestMapping(parsed)));
  saved = '{broken';
  assert.deepEqual(readMemory(storage).keys, []);
});
