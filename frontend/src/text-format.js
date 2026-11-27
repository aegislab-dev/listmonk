// Inline formatting (bold, underline, font size) for visual editor Text blocks (LST-77).
//
// A Text block stores its content in `props.text` and the upstream renderer
// (@usewaypoint/block-text) either escapes it (markdown off) or renders it as
// Markdown with a small HTML allow-list (b, strong, u, span[style], br, div, ...).
// Formatting is therefore stored as inline HTML inside the Markdown source, so the
// unchanged upstream renderer, the editor preview and an older image all render it.
//
// Three storage modes:
// - plain:    markdown off. No formatting. Text is escaped by the renderer.
// - markdown: markdown on. Text is Markdown source; formatting is inline tags in it.
// - html:     markdown on and the text is one HTML block written by this editor,
//             <div class="axm-text">…</div>, with escaped text and <br> for line
//             breaks. A plain block turns into this when it gets its first formatting,
//             so its characters keep their literal meaning (no Markdown syntax) and its
//             line breaks show as typed. Marked passes a one-line HTML block through
//             untouched.

export const FONT_SIZES = [12, 14, 16, 18, 20, 24, 28, 32];
const MIN_SIZE = 8;
const MAX_SIZE = 72;

const HTML_OPEN = '<div class="axm-text">';
const HTML_CLOSE = '</div>';

export const textMode = (text, markdown) => {
  if (!markdown) return 'plain';
  const t = text || '';
  return t.startsWith(HTML_OPEN) && t.endsWith(HTML_CLOSE) && !t.includes('\n') ? 'html' : 'markdown';
};

const ENTITIES = {
  amp: '&', lt: '<', gt: '>', quot: '"', '#39': "'", '#x27': "'", nbsp: '\u00a0',
};
const decode = (s) => s.replace(/&(amp|lt|gt|quot|#39|#x27|nbsp);/g, (_, e) => ENTITIES[e]);
const escape = (s) => s.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;');

const validSize = (n) => Number.isInteger(n) && n >= MIN_SIZE && n <= MAX_SIZE;
const NONE = { bold: false, underline: false, size: null };
const same = (a, b) => a.bold === b.bold && a.underline === b.underline && a.size === b.size;
export const isFormatted = (seg) => Boolean(seg.bold || seg.underline || validSize(seg.size));

// The opening of a run this editor writes: size span, then strong, then u, in that
// order. Its closing is the same tags reversed, on the same line.
const OPEN = /<span style="font-size:(\d+)px">(<strong>)?(<u>)?|<strong>(<u>)?|<u>/g;
const OURS = /<\/?(?:strong|u)>|<span style="font-size:\d+px">|<\/span>|<br>/;
// A Markdown line starts with its block markers (list, quote, heading, indent): the
// serializer keeps them in front of the tags.
const MARKERS = /^(?:[ \t]*(?:[-*+][ \t]+|\d{1,9}[.)][ \t]+|>[ \t]?|#{1,6}[ \t]+))*[ \t]*/;

// Markdown code (fenced blocks, indented blocks and `code spans`) shows tags as text:
// the ranges of the source where a tag is not formatting. An indented line after a
// list item is taken as the item's continuation, not as code.
const codeRanges = (src) => {
  const ranges = [];
  let pos = 0;
  const lines = src.split('\n');
  let fence = null;
  let start = 0;
  let prevBlank = true;
  let indented = false;
  // The column where the content of the current list item starts, or -1 outside a
  // list. Code inside an item needs four columns more than that.
  let item = -1;
  const width = (ws) => [...ws].reduce((n, ch) => (ch === '\t' ? n + 4 - (n % 4) : n + 1), 0);
  lines.forEach((line) => {
    const m = /^ {0,3}(`{3,}|~{3,})/.exec(line);
    const blank = !line.trim();
    const lead = width(/^[ \t]*/.exec(line)[0]);
    const base = item < 0 ? 0 : item;
    if (fence) {
      if (m && m[1][0] === fence[0] && m[1].length >= fence.length && !line.slice(m[0].length).trim()) {
        ranges.push([start, pos + line.length]);
        fence = null;
      }
    } else if (m) {
      [, fence] = m;
      start = pos;
      indented = false;
    } else if (!blank && lead >= base + 4 && (indented || prevBlank)) {
      // An indented code block starts after a blank line (it cannot interrupt a
      // paragraph) and runs on over blank lines while its lines stay indented.
      indented = true;
      ranges.push([pos, pos + line.length]);
    } else if (!blank) {
      indented = false;
      const li = /^([ \t]*)([-*+]|\d{1,9}[.)])([ \t]+|$)/.exec(line);
      if (li && width(li[1]) < base + 4) {
        // Content starts after the marker and its spaces (one when there are five or more).
        const gap = width(li[3]);
        item = width(li[1]) + li[2].length + (gap > 4 || gap === 0 ? 1 : gap);
      } else if (lead < base) item = prevBlank || item < 0 ? -1 : item;
    }
    prevBlank = blank;
    pos += line.length + 1;
  });
  if (fence) ranges.push([start, src.length]);
  // Code spans: a run of n backticks up to the next run of exactly n, within a paragraph.
  const outside = (i) => !ranges.some(([a, b]) => i >= a && i < b);
  const ticks = /`+/g;
  for (let m = ticks.exec(src); m; m = ticks.exec(src)) {
    let escaped = 0;
    for (let i = m.index - 1; i >= 0 && src[i] === '\\'; i -= 1) escaped += 1;
    if (escaped % 2 === 0 && outside(m.index)) {
      const close = new RegExp(`(?<!\`)\`{${m[0].length}}(?!\`)`, 'g');
      close.lastIndex = m.index + m[0].length;
      const end = close.exec(src);
      if (end && !src.slice(m.index, end.index).includes('\n\n') && outside(end.index)) {
        ranges.push([m.index, end.index + end[0].length]);
        ticks.lastIndex = end.index + end[0].length;
      }
    }
  }
  return ranges;
};

// Splits stored text into formatted segments: [{ value, bold, underline, size }].
//
// Reading back must never change text nobody edited (LST-77 review: code examples,
// lists, hand-written HTML). So only what serializeFormatted itself would write is
// formatting: a whole run <span style="font-size:N"><strong><u>text</u></strong></span>
// (any of the three, in that order) on one line, with none of these tags inside, not
// starting with a line's block markers, and not right after an equal run. Everything
// else, also inside Markdown code, stays source text and is written back as it was.
export const parseFormatted = (text, mode) => {
  let src = text || '';
  if (mode === 'plain') return src ? [{ value: src, ...NONE }] : [];
  const html = mode === 'html';
  if (html) src = src.slice(HTML_OPEN.length, src.length - HTML_CLOSE.length);
  const read = (s) => (html ? decode(s.replace(/<br>/g, '\n')) : s);

  const out = [];
  const push = (value, f = NONE) => {
    if (!value) return;
    const last = out[out.length - 1];
    if (last && !isFormatted(f) && !isFormatted(last)) last.value += value;
    else out.push({ value, ...f });
  };
  const code = html ? [] : codeRanges(src);
  const inCode = (i) => code.some(([a, b]) => i >= a && i < b);
  let pos = 0;
  OPEN.lastIndex = 0;
  for (let m = OPEN.exec(src); m; m = OPEN.exec(src)) {
    const span = m[1] !== undefined;
    const f = {
      size: span ? Number(m[1]) : null,
      bold: span ? Boolean(m[2]) : m[0].startsWith('<strong>'),
      underline: span ? Boolean(m[3]) : Boolean(m[4]) || m[0] === '<u>',
    };
    const close = (f.underline ? '</u>' : '') + (f.bold ? '</strong>' : '') + (span ? '</span>' : '');
    const start = m.index + m[0].length;
    const end = src.indexOf(close, start);
    const content = end < 0 ? '' : src.slice(start, end);
    const lineStart = !html && (m.index === 0 || src[m.index - 1] === '\n');
    const last = out[out.length - 1];
    const ok = content && (!span || validSize(f.size)) && !content.includes('\n') && !OURS.test(content)
      && !inCode(m.index) && !inCode(end)
      && !(lineStart && MARKERS.exec(content)[0])
      && !(pos === m.index && last && isFormatted(last) && same(last, f));
    if (ok) {
      push(read(src.slice(pos, m.index)));
      push(read(content), f);
      pos = end + close.length;
      OPEN.lastIndex = pos;
    }
  }
  push(read(src.slice(pos)));
  return out;
};

export const hasFormatting = (text, mode) => parseFormatted(text, mode).some(isFormatted);

// Writes runs back to stored text. A run is { type: 'text', value } or
// { type: 'token', raw } with bold, underline, size. Returns { text, markdown }.
export const serializeFormatted = (runs, mode) => {
  const formatted = runs.some((r) => isFormatted(r) && (r.type === 'token' || r.value.replace(/\n/g, '')));
  if (mode === 'plain' && !formatted) {
    return { text: runs.map((r) => (r.type === 'token' ? r.raw : r.value)).join(''), markdown: false };
  }
  const html = mode !== 'markdown';
  const esc = (s) => (html ? escape(s) : s);
  const nl = html ? '<br>' : '\n';

  // Group consecutive runs with the same formatting.
  const groups = [];
  runs.forEach((r) => {
    const value = r.type === 'token' ? r.raw : r.value;
    if (!value) return;
    const piece = { token: r.type === 'token', value };
    const last = groups[groups.length - 1];
    if (last && same(last, r)) last.pieces.push(piece);
    else {
      groups.push({
        bold: Boolean(r.bold), underline: Boolean(r.underline), size: validSize(r.size) ? r.size : null, pieces: [piece],
      });
    }
  });

  // Block markers stay in front of the tags, or the list or heading would be lost.
  let text = '';
  groups.forEach((g) => {
    const open = (g.size ? `<span style="font-size:${g.size}px">` : '') + (g.bold ? '<strong>' : '') + (g.underline ? '<u>' : '');
    const end = (g.underline ? '</u>' : '') + (g.bold ? '</strong>' : '') + (g.size ? '</span>' : '');
    // Tags never span a line break: a Markdown blank line would cut them apart.
    const lines = [''];
    g.pieces.forEach((p) => {
      if (p.token) { lines[lines.length - 1] += p.value; return; }
      p.value.split('\n').forEach((part, i) => {
        if (i > 0) lines.push('');
        lines[lines.length - 1] += esc(part);
      });
    });
    text += lines.map((l, i) => {
      if (!l || !(open || end)) return l;
      const lineStart = i > 0 || text === '' || text.endsWith('\n');
      const lead = !html && lineStart ? MARKERS.exec(l)[0] : '';
      return l.length > lead.length ? lead + open + l.slice(lead.length) + end : l;
    }).join(nl);
  });
  return { text: html ? HTML_OPEN + text + HTML_CLOSE : text, markdown: true };
};
