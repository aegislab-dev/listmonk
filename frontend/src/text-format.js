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

import { markdownLanguage } from '@codemirror/lang-markdown';
import { Lexer } from 'marked';

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
const OURS = /<\/?(?:strong|u)>|<span style="font-size:\d+px">|<\/span>/;
// A Markdown line starts with its block markers (list, quote, heading, indent): the
// serializer keeps them in front of the tags.
const MARKERS = /^(?:[ \t]*(?:[-*+][ \t]+|\d{1,9}[.)][ \t]+|>[ \t]?|#{1,6}[ \t]+))*[ \t]*/;

const CODE = new Set(['CodeBlock', 'FencedCode', 'InlineCode']);
const LINK_STRUCTURE = new Set(['Autolink', 'LinkReference', 'Image']);

const {
  link, reflink, nolink, tag,
} = Lexer.rules.inline.breaks;
const isEscaped = (source, start) => {
  let escapeStart = start;
  while (source[escapeStart - 1] === '\\') escapeStart -= 1;
  return (start - escapeStart) % 2 === 1;
};

const bracketLinkRanges = (source, start, tail, tokenizer, references, collapsedLinks) => {
  if (isEscaped(source, start)) return [];
  const direct = tokenizer.link(tail);
  const token = direct || tokenizer.reflink(tail, references);
  if (token?.type !== 'link' && token?.type !== 'image') return [];
  const end = start + token.raw.length;
  const label = direct ? link.exec(token.raw) : reflink.exec(token.raw);
  // The display label can contain HTML. Keep its brackets and destination
  // intact. A collapsed reference needs an explicit key when its label changes.
  if (token.type === 'link' && label) {
    return [[start, start + 1], [start + 1 + label[1].length, end]];
  }
  const collapsed = token.type === 'link' && token.raw.endsWith('][]') && nolink.exec(token.raw);
  if (collapsed) {
    const [, key] = collapsed;
    const labelEnd = start + 1 + key.length;
    collapsedLinks.push({
      start: start + 1, end: labelEnd, offset: end - 1, text: key.replace(/\s+/g, ' '),
    });
    return [[start, start + 1], [labelEnd, end]];
  }
  return [[start, end]];
};

// Text blocks render with Marked in GFM breaks mode. Its URL tokenizer includes
// single-label hosts, queries and fragments that the editor grammar can miss.
// Its link tokenizer also distinguishes a bracketed phrase from a reference link.
const inlineSyntaxRanges = (source, protectedRanges, cells, collapsedLinks) => {
  const lexer = new Lexer({ gfm: true, breaks: true });
  const { tokenizer } = lexer;
  const references = source.includes(']:') ? lexer.lex(source).links : lexer.tokens.links;
  const starts = /<|!?\[|(?:ftp|https?):\/\/|www\.|@/gi;
  const ranges = [];
  let protectedIndex = 0;
  let cellIndex = 0;
  for (let match = starts.exec(source); match; match = starts.exec(source)) {
    let start = match.index;
    // An escaped exclamation point can still be followed by a valid link.
    if (match[0] === '![' && isEscaped(source, start)) start += 1;
    if (match[0] === '@') {
      while (start > 0 && /[A-Za-z0-9._+-]/.test(source[start - 1])) start -= 1;
    }
    while (protectedRanges[protectedIndex]?.[1] <= start) protectedIndex += 1;
    const protectedRange = protectedRanges[protectedIndex];
    if (!protectedRange || start < protectedRange[0]) {
      while (cells[cellIndex]?.[1] <= start) cellIndex += 1;
      const cell = cells[cellIndex];
      const tail = source.slice(start, cell?.[0] <= start ? cell[1] : undefined);
      let found;
      if (match[0] === '<') {
        const markup = !isEscaped(source, start) && tag.exec(tail)?.[0];
        // Plain HTML line breaks can take the same style as the text around them.
        found = markup && !/^<br\s*\/?>$/i.test(markup) ? [[start, start + markup.length]] : [];
      } else if (match[0].endsWith('[')) {
        found = bracketLinkRanges(source, start, tail, tokenizer, references, collapsedLinks);
      } else {
        const url = tokenizer.url(tail);
        found = url ? [[start, start + url.raw.length]] : [];
      }
      if (found.length) {
        const [, end] = found[found.length - 1];
        starts.lastIndex = end;
        ranges.push(...found);
      }
    }
  }
  return ranges;
};

const markdownRanges = (source, formatting, collapsedLinks = [], emphasis = []) => {
  const ranges = [];
  const cells = [];
  markdownLanguage.parser.parse(source).iterate({
    // Emphasis and strikethrough, innermost first: its opening and closing marks and
    // what is between.
    leave(node) {
      const markName = { Emphasis: 'EmphasisMark', StrongEmphasis: 'EmphasisMark', Strikethrough: 'StrikethroughMark' }[node.name];
      if (!formatting || !markName) return;
      const marks = node.node.getChildren(markName);
      if (marks.length < 2) return;
      const open = marks[0];
      const close = marks[marks.length - 1];
      if (open.to < close.from) {
        emphasis.push({
          a: open.from, b: open.to, c: close.from, d: close.to,
        });
      }
    },
    enter(node) {
      if (CODE.has(node.name) || (formatting && LINK_STRUCTURE.has(node.name))) {
        ranges.push([node.from, node.to]);
        return false;
      }
      // A thematic break (---, ***, ___, "- - -") and the underline of a Setext
      // heading (===, ---) are only syntax when nothing is around them: a tag in
      // front turns them into text (or, for "- - -", into a list).
      if (formatting && (node.name === 'HorizontalRule'
        || (node.name === 'HeaderMark' && node.node.parent?.name.startsWith('SetextHeading')))) {
        ranges.push([node.from, node.to]);
        return false;
      }
      // Block markers (#, -, 1., >, [ ]): a tag whose edge falls inside one, or right
      // after it, turns the line into plain text. The marker, its indent and the
      // spaces after it keep their format.
      if (formatting && (node.name === 'ListMark' || node.name === 'QuoteMark' || node.name === 'TaskMarker'
        || (node.name === 'HeaderMark' && node.node.parent?.name.startsWith('ATXHeading') && !node.node.prevSibling))) {
        const lineStart = source.lastIndexOf('\n', node.from - 1) + 1;
        let { to } = node;
        while (source[to] === ' ' || source[to] === '\t') to += 1;
        ranges.push([lineStart, to]);
        return undefined;
      }
      if (formatting && node.name === 'Table') {
        // Preserve everything between cells, including the alignment row and
        // whitespace next to pipes. Cell text is still eligible for formatting.
        let gap = node.from;
        const rows = [...node.node.getChildren('TableHeader'), ...node.node.getChildren('TableRow')];
        rows.forEach((row) => row.getChildren('TableCell').forEach((cell) => {
          if (cell.from > gap) ranges.push([gap, cell.from]);
          cells.push([cell.from, cell.to]);
          gap = cell.to;
        }));
        if (gap < node.to) ranges.push([gap, node.to]);
      }
      return undefined;
    },
  });
  ranges.sort(([a], [b]) => a - b);
  if (!formatting) return ranges;
  const inline = inlineSyntaxRanges(source, ranges, cells, collapsedLinks);
  const combined = [...ranges, ...inline].sort(([a], [b]) => a - b);
  const disjoint = [];
  combined.forEach(([from, to]) => {
    const previous = disjoint[disjoint.length - 1];
    if (previous && from < previous[1]) previous[1] = Math.max(previous[1], to);
    else disjoint.push([from, to]);
  });
  return disjoint;
};

export const isMarkdownSyntaxAt = (source, offset) => {
  const collapsedLinks = [];
  const ranges = markdownRanges(source, true, collapsedLinks);
  return ranges.some(([from, to]) => from < offset && offset < to)
    || collapsedLinks.some(({ start, end }) => start <= offset && offset <= end);
};

const runValue = (r) => (r.type === 'token' ? r.raw : r.value);

const insertReferenceKeys = (runs, insertions) => {
  const out = [];
  let offset = 0;
  let index = 0;
  runs.forEach((run) => {
    const end = offset + runValue(run).length;
    if (run.type === 'text') {
      let from = 0;
      while (insertions[index]?.offset < end) {
        const { offset: at, text } = insertions[index];
        const to = at - offset;
        if (from < to) out.push({ ...run, value: run.value.slice(from, to) });
        out.push({ type: 'text', value: text, ...NONE });
        from = to;
        index += 1;
      }
      if (from < run.value.length) out.push({ ...run, value: run.value.slice(from) });
    } else {
      out.push(run);
    }
    offset = end;
  });
  return out;
};

const styleAt = (runs) => {
  const out = [];
  runs.forEach((r) => {
    const style = { bold: Boolean(r.bold), underline: Boolean(r.underline), size: validSize(r.size) ? r.size : null };
    for (let i = runValue(r).length; i > 0; i -= 1) out.push(style);
  });
  return out;
};
// CommonMark: a delimiter run next to a letter, digit or the like is flanked by it.
const isEdge = (ch) => ch === undefined || /[\s\p{P}\p{S}]/u.test(ch);

// A tag between an emphasis mark and its text turns the mark's neighbour into
// punctuation, and before a letter (a Korean particle: **특가**를) the mark no longer
// opens or closes the emphasis. When the format covers the whole emphasis the tags go
// around the marks; when it covers only its start or end, next to a letter, it is not
// applied there.
//
// Tags must also nest with the emphasis element: `**<u>특가**를</u>` makes marked emit
// <strong><u>특가</strong>를</u>, and the sanitizer of the mail renderer cuts that
// short, dropping the bold or the underline. A formatted run that starts outside the
// emphasis and ends inside it (or the other way round) is such a tag.
const keepEmphasis = (source, before, runs, emphasis) => {
  const now = styleAt(runs);
  const old = styleAt(before);
  const edge = (arr, i) => !same(arr[i - 1], arr[i]);
  // Does a formatted run of arr start outside the emphasis element and end inside it
  // (or start inside it and end outside)? Inside means after the opening mark and
  // before the closing one.
  const crosses = (arr, {
    a, b, c, d,
  }) => {
    let from = 0;
    for (let i = 1; i <= arr.length; i += 1) {
      if (i === arr.length || !same(arr[i], arr[from])) {
        if (isFormatted(arr[from])) {
          if (from <= a && i >= b && i <= c) return true;
          if (from >= b && from <= c && i >= d) return true;
        }
        from = i;
      }
    }
    return false;
  };
  // Returns whether it changed the styles.
  const settle = (mark) => {
    const {
      a, b, c, d,
    } = mark;
    // A tag boundary the format adds: in a mark, or right after the opening mark as a
    // closing tag, or right before the closing mark as an opening tag, or on the
    // inside next to a letter, no longer lets the marks open and close the emphasis.
    const fresh = (p) => edge(now, p) && !edge(old, p);
    let bad = false;
    for (let p = a + 1; p < b; p += 1) bad = bad || fresh(p);
    for (let p = c + 1; p < d; p += 1) bad = bad || fresh(p);
    // What is next to the whole run of marks counts (***x*** is one run of marks).
    let runStart = a;
    while (runStart > 0 && /[*_~]/.test(source[runStart - 1])) runStart -= 1;
    let runEnd = d;
    while (/[*_~]/.test(source[runEnd] || '')) runEnd += 1;
    if (fresh(b) && (isFormatted(now[b - 1]) || !isEdge(source[runStart - 1]))) bad = true;
    if (fresh(c) && (isFormatted(now[c]) || !isEdge(source[runEnd]))) bad = true;
    // Marked does not pair the outer marks of **<u>가</u>*나*다**라 (a tag next to the
    // opening mark and an emphasis inside), so such a tag is not kept there.
    if ((fresh(b) || fresh(c)) && emphasis.some((inner) => inner !== mark && inner.a > a && inner.d < d)) bad = true;
    if (!bad && crosses(now, mark) && !crosses(old, mark)) bad = true;
    if (!bad) return false;
    // The same format all through the words: the tags go around the marks. Otherwise
    // the emphasis keeps what it had.
    let whole = true;
    for (let i = b; whole && i < c; i += 1) whole = same(now[i], now[b]);
    let moved = false;
    for (let i = a; i < d; i += 1) {
      let style = old[i];
      if (whole) style = i < b || i >= c ? now[b] : now[i];
      if (!same(style, now[i])) {
        now[i] = style;
        moved = true;
      }
    }
    return moved;
  };
  // Putting an emphasis back can leave another one, around or inside it, crossing.
  let changed = false;
  for (let pass = 0, again = true; again && pass <= emphasis.length; pass += 1) {
    again = emphasis.map(settle).includes(true);
    changed = changed || again;
  }
  if (!changed) return runs;
  const out = [];
  let offset = 0;
  runs.forEach((r) => {
    const { length } = runValue(r);
    if (r.type === 'token') out.push({ ...r, ...now[offset] });
    else {
      let from = 0;
      for (let i = 1; i <= length; i += 1) {
        if (i === length || !same(now[offset + i], now[offset + from])) {
          out.push({ type: 'text', value: r.value.slice(from, i), ...now[offset + from] });
          from = i;
        }
      }
    }
    offset += length;
  });
  return out;
};

// Preserve the previous formatting in Markdown ranges where introducing tags could
// change the rendered structure or literal text. Offsets are in the original source,
// so they still line up after Chromium splits nodes while applying a format command.
export const preserveMarkdownSyntax = (before, after) => {
  const source = before.map(runValue).join('');
  if (after.map(runValue).join('') !== source) return { runs: before, insertions: [] };
  const collapsedLinks = [];
  const emphasis = [];
  const protectedRanges = markdownRanges(source, true, collapsedLinks, emphasis);
  if (!protectedRanges.length && !emphasis.length) return { runs: after, insertions: [] };

  let offset = 0;
  let rangeIndex = 0;
  let linkIndex = 0;
  let beforeIndex = 0;
  let beforeEnd = before.length ? runValue(before[0]).length : 0;
  let changed = false;
  const out = [];
  after.forEach((run) => {
    const value = runValue(run);
    const start = offset;
    const end = start + value.length;
    while (offset < end) {
      while (protectedRanges[rangeIndex]?.[1] <= offset) rangeIndex += 1;
      while (beforeEnd <= offset) {
        beforeIndex += 1;
        beforeEnd += runValue(before[beforeIndex]).length;
      }
      const [from, to] = protectedRanges[rangeIndex] || [Infinity, Infinity];
      // A personalization chip is indivisible, including inside code or a link.
      const protect = from <= offset || (run.type === 'token' && from < end);
      const next = run.type === 'token' ? end : Math.min(end, beforeEnd, protect ? to : from);
      const style = protect ? before[beforeIndex] : run;
      if (!same(style, run)) changed = true;
      while (collapsedLinks[linkIndex]?.end <= offset) linkIndex += 1;
      const collapsed = collapsedLinks[linkIndex];
      const previous = before[beforeIndex];
      if (collapsed && collapsed.start < next && (isFormatted(style) || isFormatted(previous)) && !same(style, previous)) {
        collapsed.changed = true;
      }
      const { bold, underline, size } = style;
      const fmt = { bold, underline, size };
      out.push(run.type === 'token' ? { ...run, ...fmt }
        : { type: 'text', value: value.slice(offset - start, next - start), ...fmt });
      offset = next;
    }
  });
  const insertions = collapsedLinks.filter((ref) => ref.changed).map(({ offset: at, text }) => ({ offset: at, text }));
  const runs = keepEmphasis(source, before, changed ? out : after, emphasis);
  return { runs: insertions.length ? insertReferenceKeys(runs, insertions) : runs, insertions };
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
  const code = html ? [] : markdownRanges(src, false);
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
    // Markdown keeps a literal <br> in the run. In an HTML block it decodes to a
    // newline, which the serializer writes between runs, so it cannot be inside one.
    const ok = content && (!span || (validSize(f.size) && String(f.size) === m[1]))
      && !content.includes('\n') && !OURS.test(content) && !(html && content.includes('<br>'))
      && !inCode(m.index) && !inCode(end)
      && (html || (!content.endsWith('\\') && !(/\\*$/.exec(src.slice(0, m.index))[0].length % 2)))
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
    return { text: runs.map(runValue).join(''), markdown: false };
  }
  const html = mode !== 'markdown';
  const esc = (s) => (html ? escape(s) : s);
  const nl = html ? '<br>' : '\n';

  // Group consecutive runs with the same formatting.
  const groups = [];
  runs.forEach((r) => {
    const value = runValue(r);
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
    let base = text;
    const joined = lines.map((l, i) => {
      if (!l || !(open || end)) return l;
      const lineStart = i > 0 || base === '' || base.endsWith('\n');
      const lead = !html && lineStart ? MARKERS.exec(l)[0] : '';
      let body = l.slice(lead.length);
      // In Markdown a backslash escapes the next character, also the "<" of a tag: a
      // run's own trailing backslashes go after its closing tag, and a single one
      // right before its opening tag goes inside.
      let tail = '';
      if (!html) {
        [tail] = /\\*$/.exec(body);
        body = body.slice(0, body.length - tail.length);
        const held = body && !lineStart ? /\\*$/.exec(base)[0] : '';
        if (held.length % 2) {
          base = base.slice(0, base.length - held.length);
          body = held + body;
        }
      }
      return body ? lead + open + body + end + tail : l;
    }).join(nl);
    text = base + joined;
  });
  return { text: html ? HTML_OPEN + text + HTML_CLOSE : text, markdown: true };
};
