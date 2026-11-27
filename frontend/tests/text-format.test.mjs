import test from 'node:test';
import assert from 'node:assert/strict';
import {
  FONT_SIZES, textMode, parseFormatted, isMarkdownSyntaxAt, preserveMarkdownSyntax, serializeFormatted, hasFormatting,
} from '../src/text-format.js';
import { parseTokens } from '../src/personalization.js';

const run = (value, fmt = {}) => ({ type: 'text', value, ...fmt });
const tok = (raw, fmt = {}) => ({ type: 'token', raw, ...fmt });
const OPEN = '<div class="axm-text">';
// What the box gives back from a parse: segments split into text and token runs.
const runsOf = (segments) => segments.flatMap(({ value, ...fmt }) => parseTokens(value).map((p) => (p.type === 'text'
  ? run(p.value, fmt) : tok(p.raw, fmt))));
const roundTrip = (text, markdown) => {
  const mode = textMode(text, markdown);
  return serializeFormatted(runsOf(parseFormatted(text, mode)), mode);
};

test('modes: plain, Markdown, and the one-line HTML block this editor writes', () => {
  assert.equal(textMode('a', false), 'plain');
  assert.equal(textMode('a', null), 'plain');
  assert.equal(textMode('**a**', true), 'markdown');
  assert.equal(textMode(`${OPEN}a</div>`, true), 'html');
  assert.equal(textMode(`${OPEN}a</div>`, false), 'plain');
  // A line break means it was not written by this editor: Markdown.
  assert.equal(textMode(`${OPEN}a\n</div>`, true), 'markdown');
  assert.deepEqual(FONT_SIZES, [12, 14, 16, 18, 20, 24, 28, 32]);
});

test('a plain block without formatting stays plain and unchanged', () => {
  const text = '안녕 {{ .Subscriber.Name }}님\n*별* _밑_ # <b>그대로</b> & 끝';
  assert.deepEqual(roundTrip(text, false), { text, markdown: false });
  assert.equal(hasFormatting(text, 'plain'), false);
});

test('the first formatting turns a plain block into an escaped HTML block', () => {
  const runs = [run('안녕 '), tok('{{ .Subscriber.Name }}', { bold: true }), run('님\n수수료 <0원> *별*', { bold: true, underline: true, size: 20 }),
    run(' & 끝')];
  const out = serializeFormatted(runs, 'plain');
  assert.deepEqual(out, {
    markdown: true,
    text: `${OPEN}안녕 <strong>{{ .Subscriber.Name }}</strong><span style="font-size:20px"><strong><u>님</u></strong></span><br>`
      + '<span style="font-size:20px"><strong><u>수수료 &lt;0원&gt; *별*</u></strong></span> &amp; 끝</div>',
  });
  assert.equal(textMode(out.text, out.markdown), 'html');
  // Read back: the same characters and formatting, the line break as a newline.
  const segs = parseFormatted(out.text, 'html');
  assert.equal(segs.map((s) => s.value).join(''), '안녕 {{ .Subscriber.Name }}님\n수수료 <0원> *별* & 끝');
  assert.deepEqual(segs.find((s) => s.value.startsWith('수수료')), {
    value: '수수료 <0원> *별*', bold: true, underline: true, size: 20,
  });
  assert.deepEqual(roundTrip(out.text, true), out);
});

test('Markdown blocks keep their source and get inline tags', () => {
  const text = '이번 달 [자세히](https://example.test@TrackLink)\n\n**굵게** <a href="{{ UnsubscribeURL }}">x</a>';
  assert.deepEqual(roundTrip(text, true), { text, markdown: true });
  const out = serializeFormatted([run('이번 달', { underline: true }), run(' 소식\n\n'), run('둘\n셋', { bold: true })], 'markdown');
  // Tags never cross a line break, so a blank line cannot cut one open.
  assert.deepEqual(out, { text: '<u>이번 달</u> 소식\n\n<strong>둘</strong>\n<strong>셋</strong>', markdown: true });
  assert.deepEqual(roundTrip(out.text, true), out);
});

test('inline HTML line breaks in Markdown can be formatted and reopened', () => {
  const value = '오늘 안내<br>내일 마감';
  const fmt = { bold: true, underline: true, size: 24 };
  const out = serializeFormatted([run(value, fmt)], 'markdown');
  assert.deepEqual(parseFormatted(out.text, 'markdown'), [{ value, ...fmt }]);
  assert.equal(hasFormatting(out.text, 'markdown'), true);
  assert.deepEqual(roundTrip(out.text, true), out);
});

test('formatting selections keeps Markdown links and literal code intact', () => {
  const before = runsOf(parseFormatted('Link https://example.test/event and coupon `WELCOME` here', 'markdown'));
  const after = [
    run('Link https://'),
    run('example.test', { bold: true }),
    run('/event and coupon `'),
    run('WELCOME', { underline: true, size: 24 }),
    run('` '),
    run('here', { bold: true }),
  ];
  const safe = preserveMarkdownSyntax(before, after);
  assert.equal(serializeFormatted(safe, 'markdown').text,
    'Link https://example.test/event and coupon `WELCOME` <strong>here</strong>');
  const source = before.map((r) => r.value).join('');
  assert.equal(isMarkdownSyntaxAt(source, source.indexOf('example')), true);
  assert.equal(isMarkdownSyntaxAt(source, source.indexOf('WELCOME')), true);
  assert.equal(isMarkdownSyntaxAt(source, source.indexOf('here')), false);
});

test('formatting a whole Markdown table wraps cell content and preserves the table syntax', () => {
  const text = '시간 | 장소\n:--- | ---:\n2시 | `서울`';
  const before = runsOf(parseFormatted(text, 'markdown'));
  const safe = preserveMarkdownSyntax(before, [run(text, { bold: true })]);
  const out = serializeFormatted(safe, 'markdown');
  assert.equal(out.text, '<strong>시간</strong> | <strong>장소</strong>\n:--- | ---:\n<strong>2시</strong> | `서울`');
  assert.deepEqual(roundTrip(out.text, true), out);
});

test('a Markdown format command keeps earlier styling within a protected link', () => {
  const url = 'https://example.test/event';
  const before = runsOf(parseFormatted(`<strong>${url}</strong> {{ .Subscriber.Name }}`, 'markdown'));
  const fmt = { bold: false, underline: false, size: 24 };
  const after = [
    run(`${url} `, fmt),
    tok('{{ .Subscriber.Name }}', fmt),
  ];
  const safe = preserveMarkdownSyntax(before, after);
  assert.equal(serializeFormatted(safe, 'markdown').text,
    `<strong>${url}</strong><span style="font-size:24px"> {{ .Subscriber.Name }}</span>`);
});

test('noncanonical font sizes stay verbatim when editing another paragraph', () => {
  const example = '<span style="font-size:020px">예시</span>';
  for (const prefix of ['-     ', '> -     ', '1.\t\t', '']) {
    const text = `안내\n\n${prefix}${example}\n\n아래 문단`;
    const runs = runsOf(parseFormatted(text, 'markdown'));
    assert.equal(hasFormatting(text, 'markdown'), false);
    runs.push(run('!'));
    assert.deepEqual(serializeFormatted(runs, 'markdown'), { text: `${text}!`, markdown: true });
  }
});

test('hand-written and broken tags', () => {
  // Only the exact tags this editor writes are formatting; the rest stays source.
  const other = '<b>a</b><span style="font-size: 18px;">b</span>';
  assert.deepEqual(parseFormatted(other, 'markdown'), [{ value: other, bold: false, underline: false, size: null }]);
  assert.deepEqual(roundTrip(other, true), { text: other, markdown: true });
  assert.deepEqual(parseFormatted('<strong>a</strong><span style="font-size:18px">b</span>', 'markdown'), [
    { value: 'a', bold: true, underline: false, size: null },
    { value: 'b', bold: false, underline: false, size: 18 },
  ]);
  // Closing tags with nothing open and sizes out of range stay as text.
  const segs = parseFormatted('a </u> </span> <span style="font-size:99px">y</span>', 'markdown');
  assert.equal(segs.length, 1);
  assert.equal(segs[0].value, 'a </u> </span> <span style="font-size:99px">y</span>');
  // An unclosed tag, overlapping tags or the editor's tags in another order are text.
  for (const s of ['<u>a', '<strong>a<u>b</strong>c</u>', '<u><strong>a</strong></u>']) {
    assert.deepEqual(roundTrip(s, true), { text: s, markdown: true }, s);
  }
  assert.deepEqual(parseFormatted('<u><strong>a</strong></u>', 'markdown').map((x) => [x.value, x.bold]),
    [['<u>', false], ['a', true], ['</u>', false]]);
});

test('formatting with nothing in it is no formatting', () => {
  assert.deepEqual(serializeFormatted([run('a'), run('\n', { bold: true })], 'plain'), { text: 'a\n', markdown: false });
  assert.deepEqual(serializeFormatted([run('a', { size: 99 })], 'plain'), { text: 'a', markdown: false });
  assert.deepEqual(serializeFormatted([run('a', { bold: true }), run('', { underline: true })], 'markdown'),
    { text: '<strong>a</strong>', markdown: true });
  // A formatted block that loses all its formatting keeps its HTML block (line breaks stay).
  assert.deepEqual(serializeFormatted([run('a\nb')], 'html'), { text: `${OPEN}a<br>b</div>`, markdown: true });
});

test('entities in the HTML block are read back as characters', () => {
  const segs = parseFormatted(`${OPEN}&amp;lt; &quot;q&quot; &#39;s&#39; a&nbsp;b</div>`, 'html');
  assert.equal(segs[0].value, '&lt; "q" \'s\' a\u00a0b');
});

test('Markdown code keeps its tags as text and is written back unchanged', () => {
  const text = '예: `<b>예시</b>`, ``a `<strong>q</strong>` b`` <strong>진짜</strong>\n```html\n<u>코드</u>\n<strong>x</strong>\n```\n<u>밖</u> \\`<u>이스케이프</u>\\`';
  const segs = parseFormatted(text, 'markdown');
  assert.deepEqual(segs.filter((s) => s.bold || s.underline).map((s) => s.value), ['진짜', '밖', '이스케이프']);
  assert.ok(segs[0].value.includes('`<b>예시</b>`') && segs[0].value.includes('`<strong>q</strong>`'));
  assert.deepEqual(roundTrip(text, true), { text, markdown: true });
  // Editing elsewhere (one more character at the end) leaves the code as it was.
  const runs = runsOf(segs);
  runs.push(run('!'));
  assert.equal(serializeFormatted(runs, 'markdown').text, `${text}!`);
  // An unclosed fence runs to the end.
  assert.equal(hasFormatting('```\n<strong>x</strong>', 'markdown'), false);
});

test('list, quote and heading markers stay in front of the tags', () => {
  assert.deepEqual(serializeFormatted([run('- 여름 상품\n- 겨울 상품', { bold: true })], 'markdown'),
    { text: '- <strong>여름 상품</strong>\n- <strong>겨울 상품</strong>', markdown: true });
  assert.deepEqual(serializeFormatted([run('1. 하나\n  2) 둘\n> 인용\n## 제목', { underline: true })], 'markdown'),
    { text: '1. <u>하나</u>\n  2) <u>둘</u>\n> <u>인용</u>\n## <u>제목</u>', markdown: true });
  // In the middle of a line they are just text.
  assert.deepEqual(serializeFormatted([run('앞 '), run('- 하이픈', { bold: true })], 'markdown'),
    { text: '앞 <strong>- 하이픈</strong>', markdown: true });
  // A line that is only a marker gets no empty tags.
  assert.deepEqual(serializeFormatted([run('- ', { bold: true }), run('x')], 'markdown'), { text: '- x', markdown: true });
  // The HTML block has no Markdown: nothing moves.
  assert.equal(serializeFormatted([run('- a', { bold: true })], 'plain').text, '<div class="axm-text"><strong>- a</strong></div>');
});

test('indented Markdown code keeps its tags as text, list continuations do not', () => {
  const text = '예문:\n\n    <u><strong>예시</strong></u>\n\n    <strong>둘째</strong>\n\n아래 <strong>본문</strong>';
  const segs = parseFormatted(text, 'markdown');
  assert.deepEqual(segs.filter((s) => s.bold || s.underline).map((s) => s.value), ['본문']);
  assert.deepEqual(roundTrip(text, true), { text, markdown: true });
  const runs = runsOf(segs);
  runs.push(run('!'));
  assert.equal(serializeFormatted(runs, 'markdown').text, `${text}!`);
  // An indented line cannot interrupt a paragraph: no blank line, not code.
  assert.equal(hasFormatting('문단\n    <strong>x</strong>', 'markdown'), true);
  // After a list item an indented paragraph belongs to the item ...
  assert.equal(hasFormatting('- 항목\n\n    <strong>x</strong>', 'markdown'), true);
  assert.equal(hasFormatting('1. 항목\n\n     <strong>x</strong>', 'markdown'), true);
  // ... and four columns past the item's content it is code in the item.
  assert.equal(hasFormatting('- 항목\n\n      <strong>x</strong>', 'markdown'), false);
  const inList = '- 항목\n\n        <u><strong>예시</strong></u>\n\n아래 문단';
  assert.equal(hasFormatting(inList, 'markdown'), false);
  const runs2 = runsOf(parseFormatted(inList, 'markdown'));
  runs2.push(run('!'));
  assert.equal(serializeFormatted(runs2, 'markdown').text, `${inList}!`);
  // A nested item and its code.
  assert.equal(hasFormatting('- a\n  - b\n\n        <strong>x</strong>', 'markdown'), false);
  // After the list ends, a plain paragraph is formatting again.
  assert.equal(hasFormatting('- a\n\n<strong>x</strong>', 'markdown'), true);
  // A tab indents too.
  assert.equal(hasFormatting('\t<strong>x</strong>', 'markdown'), false);
});

// LST-77 review 4: whatever the text, reading it back and writing it unchanged gives
// the same text, so nothing anybody did not edit (code, lists, quotes, hand-written
// HTML) can change.
test('reading back and writing unchanged is the identity, for any text', () => {
  const pieces = ['a', '가', ' ', '  ', '\n', '\n\n', '- ', '* ', '1. ', '2) ', '> ', '# ', '    ', '\t', '`', '```', '~~~',
    '<strong>', '</strong>', '<u>', '</u>', '<span style="font-size:20px">', '<span style="font-size:020px">', '<span style="font-size:99px">', '</span>',
    '<b>', '<br>', '&amp;', '&lt;', '{{ .Subscriber.Name }}', '*', '_', '<'];
  let seed = 77;
  const rand = (n) => { seed = (Math.imul(seed, 1103515245) + 12345) >>> 0; return (seed >>> 8) % n; };
  for (let i = 0; i < 5000; i += 1) {
    let s = '';
    for (let k = rand(40); k > 0; k -= 1) s += pieces[rand(pieces.length)];
    assert.deepEqual(roundTrip(s, true), { text: s, markdown: true }, JSON.stringify(s));
    // The same with one character typed at the end: only that character is added.
    const runs = runsOf(parseFormatted(s, 'markdown'));
    runs.push(run('!'));
    assert.equal(serializeFormatted(runs, 'markdown').text, `${s}!`, JSON.stringify(s));
  }
});

test('what the editor writes reads back as the same runs, in both modes', () => {
  const texts = ['a', '가 나', '- x', '  y', '<', '&', '{{ .Subscriber.Name }}', '\n', '1. z', '`q`'];
  let seed = 7;
  const rand = (n) => { seed = (seed * 1103515245 + 12345) % 2147483648; return seed % n; };
  for (let i = 0; i < 3000; i += 1) {
    const runs = [];
    for (let k = rand(8); k > 0; k -= 1) {
      runs.push(run(texts[rand(texts.length)], { bold: rand(2) === 1, underline: rand(2) === 1, size: [null, 12, 20, 32][rand(4)] }));
    }
    for (const mode of ['markdown', 'plain']) {
      const out = serializeFormatted(runs, mode);
      const m = textMode(out.text, out.markdown);
      // Written again from what was read, the text is the same.
      assert.deepEqual(roundTrip(out.text, out.markdown), out, JSON.stringify(runs));
      // And the characters are the same, in order.
      const plain = runs.map((r) => r.value).join('');
      assert.equal(parseFormatted(out.text, m).map((x) => x.value).join(''), plain, JSON.stringify(runs));
    }
  }
});
