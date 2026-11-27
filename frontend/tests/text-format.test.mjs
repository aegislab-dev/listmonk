import test from 'node:test';
import assert from 'node:assert/strict';
import {
  FONT_SIZES, textMode, parseFormatted, serializeFormatted, hasFormatting,
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

test('hand-written and broken tags', () => {
  assert.deepEqual(parseFormatted('<b>a</b><span style="font-size: 18px;">b</span>', 'markdown'), [
    { value: 'a', bold: true, underline: false, size: null },
    { value: 'b', bold: false, underline: false, size: 18 },
  ]);
  // Closing tags with nothing open and sizes out of range stay as text.
  const segs = parseFormatted('a </u> </span> <span style="font-size:99px">y</span>', 'markdown');
  assert.equal(segs.length, 1);
  assert.equal(segs[0].value, 'a </u> </span> <span style="font-size:99px">y</span>');
  // An unclosed tag formats to the end.
  assert.deepEqual(parseFormatted('<u>a', 'markdown'), [{ value: 'a', bold: false, underline: true, size: null }]);
  // Overlapping tags: each closes its own kind.
  assert.deepEqual(parseFormatted('<strong>a<u>b</strong>c</u>', 'markdown').map((s) => [s.value, s.bold, s.underline]),
    [['a', true, false], ['b', true, true], ['c', false, true]]);
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
