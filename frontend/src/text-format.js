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

const NONE = { bold: false, underline: false, size: null };
const validSize = (n) => Number.isInteger(n) && n >= MIN_SIZE && n <= MAX_SIZE;

// Exactly the tags this editor writes, so text it reads back is written back the
// same. Other HTML (<b>, other spans) stays source text in the box.
const TAG = /<(\/?)(strong|u)>|<span style="font-size:(\d+)px">|<(\/)span>|<br>/g;

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
  let inList = false;
  lines.forEach((line) => {
    const m = /^ {0,3}(`{3,}|~{3,})/.exec(line);
    const blank = !line.trim();
    if (fence) {
      if (m && m[1][0] === fence[0] && m[1].length >= fence.length && !line.slice(m[0].length).trim()) {
        ranges.push([start, pos + line.length]);
        fence = null;
      }
    } else if (m) {
      [, fence] = m;
      start = pos;
      indented = false;
      inList = false;
    } else if (!blank && /^( {4}|\t)/.test(line) && (indented || (prevBlank && !inList))) {
      // An indented code block starts after a blank line (it cannot interrupt a
      // paragraph) and runs on over blank lines while its lines stay indented.
      indented = true;
      ranges.push([pos, pos + line.length]);
    } else if (!blank) {
      indented = false;
      if (/^ {0,3}([-*+]|\d{1,9}[.)])([ \t]|$)/.test(line)) inList = true;
      else if (!/^[ \t]/.test(line)) inList = false;
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
// Tags that do not open or close one of ours stay as literal source text.
export const parseFormatted = (text, mode) => {
  let src = text || '';
  if (mode === 'plain') return src ? [{ value: src, ...NONE }] : [];
  const html = mode === 'html';
  if (html) src = src.slice(HTML_OPEN.length, src.length - HTML_CLOSE.length);

  const stack = [];
  const out = [];
  const fmt = () => ({
    bold: stack.some((t) => t.kind === 'bold'),
    underline: stack.some((t) => t.kind === 'underline'),
    size: [...stack].reverse().find((t) => t.kind === 'size')?.size ?? null,
  });
  const push = (value) => {
    if (!value) return;
    const v = html ? decode(value) : value;
    const f = fmt();
    const last = out[out.length - 1];
    if (last && last.bold === f.bold && last.underline === f.underline && last.size === f.size) last.value += v;
    else out.push({ value: v, ...f });
  };
  const close = (kind) => {
    for (let i = stack.length - 1; i >= 0; i -= 1) {
      if (stack[i].kind === kind) { stack.splice(i, 1); return true; }
    }
    return false;
  };

  const code = html ? [] : codeRanges(src);
  let pos = 0;
  TAG.lastIndex = 0;
  const tags = [];
  for (let m = TAG.exec(src); m; m = TAG.exec(src)) {
    if (!code.some(([a, b]) => m.index >= a && m.index < b)) tags.push(m);
  }
  tags.forEach((m) => {
    push(src.slice(pos, m.index));
    pos = m.index + m[0].length;
    const [raw, slash, name, size, spanClose] = m;
    if (name) {
      const kind = name === 'u' ? 'underline' : 'bold';
      if (!slash) stack.push({ kind });
      else if (!close(kind)) push(raw);
    } else if (size !== undefined) {
      const n = Number(size);
      if (validSize(n)) stack.push({ kind: 'size', size: n });
      else push(raw);
    } else if (spanClose) {
      if (!close('size')) push(raw);
    } else if (html) {
      push('\n');
    } else {
      push(raw);
    }
  });
  push(src.slice(pos));
  return out;
};

export const isFormatted = (seg) => Boolean(seg.bold || seg.underline || validSize(seg.size));
export const hasFormatting = (text, mode) => parseFormatted(text, mode).some(isFormatted);

const same = (a, b) => a.bold === b.bold && a.underline === b.underline && a.size === b.size;

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

  // A Markdown line starts with its block markers (list, quote, heading, indent):
  // they stay in front of the tags, or the list or heading would be lost.
  const MARKERS = /^(?:[ \t]*(?:[-*+][ \t]+|\d{1,9}[.)][ \t]+|>[ \t]?|#{1,6}[ \t]+))*[ \t]*/;
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
