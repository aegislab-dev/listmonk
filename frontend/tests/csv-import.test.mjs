import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import Papa from 'papaparse';
import {
  LIMITS, STORAGE_KEY, checkInputSize, cleanEmail, validEmail, suggestKey, isSpelledKey, romanize,
  parseCSV, suggestMapping, validateMapping, convertCSV, readMemory, rememberMapping,
  inspectOriginalCSV, inspectCSVHeader, standardTwin, unresolvedValues,
} from '../src/lib/csv-import.js';

// The dictionary the server sends in /api/config, read from the file the server embeds.
const STANDARD = JSON.parse(readFileSync(new URL('../../internal/core/axm_standard_attribs.json', import.meta.url), 'utf8'));
const suggest = (parsed, memory, existingKeys = []) => suggestMapping(parsed, memory, { standard: STANDARD, existingKeys });

const converted = (text, mapping, keys) => {
  const parsed = parseCSV(text);
  return convertCSV(parsed, mapping || suggest(parsed), keys, STANDARD);
};
const unpack = (result) => Papa.parse(new TextDecoder().decode(result.bytes), { header: true }).data;

test('quoted comma, escaped quotes, newline and empty fields round trip through standard CSV and JSON', () => {
  const result = converted('메일 주소,성함,회사명,관심 태그,External ID,메모\n ONE@EXAMPLE.INVALID ,"가상 ""한"",이름","첫 줄\n둘째 줄","개발, AI,개발, ,마케팅",00123,버림');
  const [row] = unpack(result);
  assert.equal(row.email, 'one@example.invalid');
  assert.equal(row.name, '가상 "한",이름');
  assert.deepEqual(JSON.parse(row.attributes), { company: '첫 줄\n둘째 줄', tags: ['개발', 'AI', '마케팅'], external_id: '00123' });
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
  // Suggested names never collide: a taken name is spelled out, then numbered.
  const twins = parseCSV('email,전화번호,휴대폰,직책,직급,회사,회사,메모2,메모2\na@example.invalid,1,2,3,4,5,6,7,8');
  assert.deepEqual(suggest(twins).map(({ key }) => key), ['', 'phone', 'hyudaepon', 'title', 'jikgeup', 'company', 'hoesa', 'memo2', 'memo2_2']);
  assert.deepEqual(validateMapping(suggest(twins)), []);
  assert.equal(isSpelledKey('휴대폰', 'hyudaepon'), true);
  assert.equal(isSpelledKey('메모2', 'memo2_2'), true);
  assert.deepEqual(suggestMapping(twins, { columns: { 휴대폰: { role: 'attribute', key: 'phone' } } }).map(({ key }) => key).slice(1, 3), ['jeonhwabeonho', 'phone']);
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
  assert.deepEqual(result.preview[0], {
    row: 2, email: 'ab@example.invalid', name: 'first\nname', attributes: [], changed: {}, dropped: [],
  });
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
  rememberMapping(storage, parsed.columns, suggest(parsed));
  assert.equal(saved.includes('private@example.invalid'), false);
  assert.equal(saved.includes('가상 이름'), false);
  assert.equal(saved.includes('비밀 값'), false);
  assert.deepEqual(readMemory(storage).keys, ['company']);
  const large = Array.from({ length: 201 }, (_, i) => `key_${i}`);
  rememberMapping(storage, large, large.map((key) => ({ role: 'attribute', key })));
  assert.equal(Object.keys(readMemory(storage).columns).length, 200);
  assert.equal(readMemory(storage).keys.length, 200);
  assert.doesNotThrow(() => rememberMapping({ getItem() { throw Error(); }, setItem() { throw Error(); } }, parsed.columns, suggestMapping(parsed)));
  saved = '{broken';
  assert.deepEqual(readMemory(storage).keys, []);
});

test('column names the standard dictionary knows get the standard key', () => {
  const names = {
    country: ['국가', '지역', '나라', '국가코드', '국가 코드', '거주 국가', 'Country', 'Country Code', 'country_code', 'Region', 'nation'],
    follower: ['팔로워', '팔로워 수', '팔로워수', '팔로어', 'Follower', 'Followers', 'Follower Count', 'follower_count', 'followers_count'],
    company: ['회사', '회사명', '기업명', '소속', '업체명', '상호', '법인명', 'Company', 'Company Name', 'company_name', 'Organization'],
    title: ['직함', '직책', '직급', '직위', 'Title', 'Job Title', 'job_title', 'Position'],
    industry: ['업종', '산업', '산업군', 'Industry', 'sector'],
  };
  Object.entries(names).forEach(([key, labels]) => labels.forEach((label) => {
    assert.equal(suggestKey(label, STANDARD), key, label);
    const parsed = parseCSV(`email,${label}\na@example.invalid,x`);
    assert.deepEqual(suggest(parsed)[1], { role: 'attribute', key }, label);
  }));
  // Names with another meaning are not collected.
  for (const label of ['소재지', '주소', '도시', 'city', 'address', '구독자 수', '평균 조회수', '참여율', '브랜드명', '광고주명', '부서', 'department', '카테고리', '업태']) {
    assert.equal(STANDARD.some(({ key }) => key === suggestKey(label, STANDARD)), false, label);
  }
  // The names this screen used to make up for these columns are gone.
  const old = parseCSV('email,팔로워,지역,회사명,직함,직급\na@example.invalid,1,2,3,4,5');
  assert.deepEqual(suggest(old).map(({ key }) => key), ['', 'follower', 'country', 'company', 'title', 'jikgeup']);
  // Without a dictionary nothing is standard and the old rules apply.
  assert.equal(suggestKey('지역'), 'jiyeok');
  // A key that reads like a standard item points at it; the item itself and other keys do not.
  assert.equal(standardTwin('followers', STANDARD), 'follower');
  assert.equal(standardTwin('company_name', STANDARD), 'company');
  assert.equal(standardTwin('job_rank', STANDARD), 'title');
  assert.equal(standardTwin('region', STANDARD), 'country');
  assert.equal(standardTwin('follower', STANDARD), '');
  assert.equal(standardTwin('gmv', STANDARD), '');
});

test('a name already in use wins over a made-up one, a standard key wins over both', () => {
  const parsed = parseCSV('email,TikTokerURL,성별,Axiom Sales ID,지역,가입 경로\na@example.invalid,u,여,7,US,web');
  const existing = ['tiktokerurl', '성별', 'axiom sales id', 'region', 'gaip'];
  assert.deepEqual(suggest(parsed, undefined, existing).map(({ key }) => key), ['', 'tiktokerurl', '성별', 'axiom sales id', 'country', 'gaip_gyeongro']);
  // Without the names in use the same columns get new names.
  assert.deepEqual(suggest(parsed).map(({ key }) => key), ['', 'tik_toker_url', 'gender', 'axiom_sales_id', 'country', 'gaip_gyeongro']);
  // An exact match is preferred among several, and one name is not given to two columns.
  const twice = parseCSV('email,Plan,plan\na@example.invalid,a,b');
  assert.deepEqual(suggest(twice, undefined, ['plan', 'Plan']).map(({ key }) => key), ['', 'Plan', 'plan']);
  assert.deepEqual(suggest(twice, undefined, ['plan']).map(({ key }) => key), ['', 'plan', 'plan_2']);
  assert.deepEqual(validateMapping(suggest(parsed, undefined, existing), existing), []);
});

test('remembered made-up names of standard columns give way; names chosen on purpose stay', () => {
  const parsed = parseCSV('email,지역,팔로워,회사명,직함,직급,업종,Followers\na@example.invalid,1,2,3,4,5,6,7');
  const memory = { columns: {
    지역: { role: 'attribute', key: 'region' },
    팔로워: { role: 'attribute', key: 'palrowo' },
    회사명: { role: 'attribute', key: 'company_name' },
    직함: { role: 'discard', key: '' },
    직급: { role: 'attribute', key: 'job_rank' },
    업종: { role: 'attribute', key: 'sector_kr' },
    Followers: { role: 'attribute', key: 'followers' },
  } };
  assert.deepEqual(suggest(parsed, memory), [
    { role: 'email', key: '' }, { role: 'attribute', key: 'country' }, { role: 'attribute', key: 'follower' },
    { role: 'attribute', key: 'company' }, { role: 'discard', key: '' }, { role: 'attribute', key: 'title' },
    { role: 'attribute', key: 'sector_kr' }, { role: 'attribute', key: 'followers' },
  ]);
});

test('values of country and follower columns are stored in the standard format', () => {
  const parsed = parseCSV([
    'email,지역,팔로워,회사명,메모',
    'a@example.invalid,United States,"14,900", (주)예시 ,x',
    'b@example.invalid,미국,10K,,x',
    'c@example.invalid,PH,1.2M,,x',
    'd@example.invalid,uk,1만,,x',
    'e@example.invalid,-,3.5천,,x',
    'f@example.invalid,,52300,,x',
    'bad,서울,약 1만,,x',
  ].join('\n'));
  const result = convertCSV(parsed, suggest(parsed), [], STANDARD);
  assert.deepEqual(unpack(result).map((row) => (row.attributes ? JSON.parse(row.attributes) : null)), [
    { country: 'US', follower: 14900, company: '(주)예시' }, { country: 'US', follower: 10000 }, { country: 'PH', follower: 1200000 },
    { country: 'GB', follower: 10000 }, { follower: 3500 }, { follower: 52300 },
  ]);
  // Numbers are JSON numbers, not text.
  assert.match(unpack(result)[0].attributes, /"follower":14900[,}]/);
  // Only rows that are imported are counted; the invalid e-mail row is not.
  assert.deepEqual(result.values.map(({ examples, conversions, ...counts }) => counts), [
    { index: 1, key: 'country', kind: 'country', kept: 1, changed: 3, blank: 1, unreadable: 0 },
    { index: 2, key: 'follower', kind: 'integer', kept: 1, changed: 5, blank: 0, unreadable: 0 },
  ]);
  assert.deepEqual(result.values[0].conversions, [{ from: 'United States', to: 'US' }, { from: '미국', to: 'US' }, { from: 'uk', to: 'GB' }]);
  assert.deepEqual(result.preview[0].changed, { country: 'United States', follower: '14,900' });
  assert.deepEqual(unresolvedValues(result.values, suggest(parsed)), []);
  // The same columns under other names are stored as written.
  const plain = suggest(parsed).map((entry) => ({ ...entry, key: { country: 'area', follower: 'fans' }[entry.key] || entry.key }));
  const kept = convertCSV(parsed, plain, [], STANDARD);
  assert.deepEqual(JSON.parse(unpack(kept)[1].attributes), { area: '미국', fans: '10K' });
  assert.deepEqual(kept.values, []);
  // A tags column is never converted, whatever its name.
  const tagged = suggest(parsed).map((entry) => (entry.key === 'country' ? { role: 'tags', key: 'country' } : entry));
  assert.deepEqual(JSON.parse(unpack(convertCSV(parsed, tagged, [], STANDARD))[0].attributes).country, ['United States']);
});

test('values that cannot be read are reported, never stored, and block the upload until left out', () => {
  const parsed = parseCSV([
    'email,지역,팔로워',
    ...['서울,100', '경기,약 2만', '부산,12.500', '서울,10K+', 'KR,-5', '대전,7', '인천,8', '광주,9'].map((cells, n) => `p${n}@example.invalid,${cells}`),
  ].join('\n'));
  const mapping = suggest(parsed);
  const result = convertCSV(parsed, mapping, [], STANDARD);
  const [country, follower] = result.values;
  assert.equal(country.unreadable, 7);
  assert.equal(country.kept, 1);
  // At most five different examples, with the row they come from.
  assert.deepEqual(country.examples, [
    { row: 2, value: '서울' }, { row: 3, value: '경기' }, { row: 4, value: '부산' }, { row: 7, value: '대전' }, { row: 8, value: '인천' },
  ]);
  assert.deepEqual(follower.examples.map(({ value }) => value), ['약 2만', '12.500', '10K+', '-5']);
  assert.deepEqual(unresolvedValues(result.values, mapping), [1, 2]);
  assert.deepEqual(unresolvedValues(result.values, [mapping[0], { ...mapping[1], blank: true }, mapping[2]]), [2]);
  assert.deepEqual(unresolvedValues(result.values, mapping.map((entry) => ({ ...entry, blank: true }))), []);
  // Unreadable cells are left out of the upload whether or not that was agreed yet.
  assert.deepEqual(unpack(result).map((row) => (row.attributes ? JSON.parse(row.attributes) : null)), [
    { follower: 100 }, null, null, null, { country: 'KR' }, { follower: 7 }, { follower: 8 }, { follower: 9 },
  ]);
  // The preview shows the first five rows; they already show values that are left out.
  assert.deepEqual(result.preview.map(({ row }) => row), [2, 3, 4, 5, 6]);
  assert.deepEqual(result.preview[1].dropped, [['country', '경기'], ['follower', '약 2만']]);
  assert.deepEqual(result.preview[0].dropped, [['country', '서울']]);
});

test('the preview adds rows with a value left out when the first five have none', () => {
  const rows = Array.from({ length: 12 }, (_, n) => `p${n}@example.invalid,${[6, 8, 9, 10].includes(n) ? '서울' : 'US'}`);
  const parsed = parseCSV(['email,국가', ...rows].join('\n'));
  const result = convertCSV(parsed, suggest(parsed), [], STANDARD);
  assert.deepEqual(result.preview.map(({ row }) => row), [2, 3, 4, 5, 6, 8, 10, 11]);
  assert.deepEqual(result.preview.map(({ dropped }) => dropped.length), [0, 0, 0, 0, 0, 1, 1, 1]);
});
