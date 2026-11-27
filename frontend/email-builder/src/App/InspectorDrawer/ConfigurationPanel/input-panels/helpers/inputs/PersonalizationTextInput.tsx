import React, { useEffect, useId, useRef, useState } from 'react';
import { Add, FormatBold, FormatClear, FormatUnderlined, Refresh } from '@mui/icons-material';
import {
  Box, Button, CircularProgress, IconButton, List, ListItemButton, MenuItem, Popover, Select, Stack, TextField,
  ToggleButton, Tooltip, Typography,
} from '@mui/material';
import {
  chipLabel, chipStyle, FormatRun, ItemResult, Personalization, PersonalizationItem, TextMode,
} from '../../../../../personalization';

type Props = {
  config: Personalization;
  label: string;
  rows: number;
  defaultValue: string;
  // Text blocks only (LST-77): bold, underline and font size on part of the text.
  formatting?: boolean;
  markdown?: boolean | null;
  onChange: (text: string, markdown?: boolean) => void;
};

type Fmt = { bold: boolean; underline: boolean; size: number | null };
const NO_FMT: Readonly<Fmt> = Object.freeze({ bold: false, underline: false, size: null });
type SelectionOffsets = { start: number; end: number };
const TYPING = new Set(['insertText', 'deleteContentBackward', 'deleteContentForward']);

// The formatting an element adds to what it contains. Chromium's bold/underline
// commands write <b>/<u> or, when undoing them inside a tag, a style; sizes are
// spans this editor writes (data-axm-size, "" = back to the block's size).
function elementFmt(el: HTMLElement, f: Fmt): Fmt {
  const n = { ...f };
  if (el.tagName === 'B' || el.tagName === 'STRONG') n.bold = true;
  if (el.tagName === 'U') n.underline = true;
  const weight = el.style.fontWeight;
  if (weight) n.bold = weight === 'bold' || weight === 'bolder' || Number(weight) >= 600;
  const deco = el.style.textDecorationLine || el.style.textDecoration;
  if (deco) n.underline = deco.includes('underline');
  if (el.dataset.axmSize === '') n.size = null;
  else if (el.dataset.axmSize !== undefined) {
    // removeFormat strips the style but leaves the mark: the style is what counts.
    const px = /^(\d+)px$/.exec(el.style.fontSize);
    if (px) n.size = Number(px[1]);
  }
  return n;
}

function fmtAt(node: Node | null, root: HTMLElement): Fmt {
  const chain: HTMLElement[] = [];
  for (let n: Node | null = node; n && n !== root; n = n.parentNode) {
    if (n instanceof HTMLElement) chain.unshift(n);
  }
  return chain.reduce((f, el) => elementFmt(el, f), { ...NO_FMT });
}

function wrap(node: Node, f: Fmt) {
  let out = node;
  if (f.underline) { const u = document.createElement('u'); u.append(out); out = u; }
  if (f.bold) { const b = document.createElement('strong'); b.append(out); out = b; }
  if (f.size) { const s = sizeSpan(f.size); s.append(out); out = s; }
  return out;
}

function sizeSpan(size: number | null) {
  const s = document.createElement('span');
  s.dataset.axmSize = size ? String(size) : '';
  s.style.fontSize = size ? `${size}px` : '14px';
  return s;
}

type Attr = 'bold' | 'underline' | 'size';

// Moves node out of its ancestor anc: anc keeps what was before it, a copy of anc
// takes what was after it, and node (with the elements between them) sits between.
function isolate(node: Node, anc: HTMLElement) {
  const after = document.createRange();
  after.setStartAfter(node);
  after.setEndAfter(anc.lastChild!);
  const tail = anc.cloneNode(false) as HTMLElement;
  tail.append(after.extractContents());
  const mid = document.createRange();
  mid.setStartBefore(node);
  mid.setEndAfter(anc.lastChild!);
  const middle = mid.extractContents();
  anc.after(middle, tail);
  if (!tail.textContent && !tail.querySelector('[data-token]')) tail.remove();
  if (!anc.textContent && !anc.querySelector('[data-token]')) anc.remove();
}

// Gives a personalization chip one formatting value. Chromium's commands skip
// non-editable chips, so a selection that holds one formats the text around it only.
function setChipFmt(chip: HTMLElement, root: HTMLElement, attr: Attr, value: boolean | number | null) {
  const gives = (el: HTMLElement) => (attr === 'size' ? el.dataset.axmSize !== undefined && el.dataset.axmSize !== ''
    : elementFmt(el, NO_FMT)[attr] === true);
  for (let guard = 0; guard < 20 && fmtAt(chip, root)[attr] !== value; guard += 1) {
    let anc: HTMLElement | null = null;
    for (let n = chip.parentElement; n && n !== root; n = n.parentElement) if (gives(n)) { anc = n; break; }
    if (!value) {
      if (!anc) break;
      isolate(chip, anc);
      continue;
    }
    const wrapper = attr === 'size' ? sizeSpan(value as number) : document.createElement(attr === 'bold' ? 'strong' : 'u');
    chip.replaceWith(wrapper);
    wrapper.append(chip);
  }
}

// Whether a range really covers part of a text node (not just touches its edge).
function covers(range: Range, node: Text) {
  if (!range.intersectsNode(node)) return false;
  const start = node === range.startContainer ? range.startOffset : 0;
  const end = node === range.endContainer ? range.endOffset : node.length;
  return node.data.slice(start, end).replace(/\u200b/g, '') !== '';
}

// This DOM is owned by the browser while editing so React never resets an IME
// composition, selection or native undo transaction on a document update.
function collect(node: Node, f: Fmt, out: FormatRun[]) {
  const text = (value: string, g: Fmt = f) => { if (value) out.push({ type: 'text', value, ...g }); };
  if (node.nodeType === Node.TEXT_NODE) {
    // Only the caret holder of an empty size span is not text; a zero-width space
    // that was in the stored text stays.
    const value = node.textContent || '';
    const holder = node.parentElement?.dataset.axmCaret !== undefined && node === node.parentElement.firstChild;
    text(holder && value.startsWith('\u200b') ? value.slice(1) : value);
    return;
  }
  if (node instanceof HTMLElement) {
    if (node.dataset.token !== undefined) { out.push({ type: 'token', raw: node.dataset.token, ...f }); return; }
    if (node.tagName === 'BR') { text('\n', NO_FMT); return; }
  }
  // Chromium also creates block wrappers when insertText contains a newline.
  // A lone <br> is an empty-line caret placeholder, not an extra stored newline.
  if (node instanceof HTMLElement && node.childNodes.length === 1 && node.firstChild?.nodeName === 'BR') return;
  const inner = node instanceof HTMLElement ? elementFmt(node, f) : f;
  Array.from(node.childNodes).forEach((child, index, children) => {
    const isBlock = (n: Node) => n instanceof HTMLElement && /^(DIV|P)$/.test(n.tagName);
    if (isBlock(child) && index > 0) text('\n', NO_FMT);
    collect(child, inner, out);
    if (isBlock(child) && children[index + 1] && !isBlock(children[index + 1])) text('\n', NO_FMT);
  });
}

function runsOf(node: Node, f: Fmt = NO_FMT) {
  const out: FormatRun[] = [];
  collect(node, f, out);
  return out;
}

const plainText = (runs: FormatRun[]) => runs.map((r) => (r.type === 'token' ? r.raw : r.value)).join('');
const serialize = (node: Node) => plainText(runsOf(node));

function makeChip(config: Personalization, item: PersonalizationItem, fallback: string, raw: string) {
  const chip = document.createElement('span');
  chip.contentEditable = 'false';
  chip.dataset.token = raw;
  chip.tabIndex = config.disabled ? -1 : 0;
  chip.setAttribute('role', 'button');
  chip.setAttribute('aria-label', `${config.strings.edit}: ${chipLabel(config, item, fallback)}`);
  chip.textContent = chipLabel(config, item, fallback);
  Object.assign(chip.style, chipStyle, { cursor: 'pointer' });
  return chip;
}

export default function PersonalizationTextInput({
  config, label, rows, defaultValue, formatting = false, markdown, onChange,
}: Props) {
  const format = formatting ? config.format : undefined;
  const mode = useRef<TextMode>(format ? format.textMode(defaultValue, markdown) : 'plain');
  const [active, setActive] = useState<Fmt>(NO_FMT);
  const id = useId();
  const editor = useRef<HTMLDivElement>(null);
  const button = useRef<HTMLButtonElement>(null);
  const lastRange = useRef<Range | null>(null);
  const composing = useRef(false);
  const mounted = useRef(false);
  const request = useRef(0);
  const pendingFocus = useRef<(() => void) | null>(null);
  const [anchor, setAnchor] = useState<HTMLElement | null>(null);
  const [editing, setEditing] = useState<HTMLElement | null>(null);
  const [items, setItems] = useState<ItemResult>({ items: config.baseItems, noLists: false, error: false, truncated: false });
  const [loading, setLoading] = useState(false);
  const [search, setSearch] = useState('');
  const [selected, setSelected] = useState<PersonalizationItem | null>(null);
  const [fallback, setFallback] = useState('');
  const s = config.strings;

  const fill = (text: string, m: TextMode) => {
    const el = editor.current!;
    const segments = format ? format.parseFormatted(text, m) : [{ value: text, ...NO_FMT }];
    el.replaceChildren();
    for (const seg of segments) {
      for (const part of config.parseTokens(seg.value)) {
        el.append(wrap(part.type === 'text' ? document.createTextNode(part.value)
          : makeChip(config, part.item, part.fallback, part.raw), seg));
      }
    }
  };

  // Undo/redo of a formatting box is this box's own (LST-77 review): the formatting
  // commands also move chips and spans by hand, and the browser's undo would replay
  // its records over that changed DOM and delete text. A state is the stored text and
  // the selection as character offsets (a chip counts as one).
  // undo: where the selection was when the edit that led to this state began, so an
  // undo puts the caret back there.
  type Snap = SelectionOffsets & { text: string; mode: TextMode; undo: SelectionOffsets };
  const onChangeRef = useRef(onChange);
  onChangeRef.current = onChange;
  const before = useRef<SelectionOffsets | null>(null);
  // Fallback for browser edits that dispatch input without beforeinput.
  const seen = useRef<SelectionOffsets | null>(null);
  const history = useRef<Snap[]>([]);
  const at = useRef(-1);
  const lastEdit = useRef({ kind: '', time: 0 });
  const offsetOf = (node: Node, offset: number, source = false) => {
    const r = document.createRange();
    r.setStart(editor.current!, 0);
    r.setEnd(node, offset);
    const runs = runsOf(r.cloneContents());
    return source ? plainText(runs).length : runs.reduce((n, x) => n + (x.type === 'token' ? 1 : x.value.length), 0);
  };
  const pointAt = (root: Node, n: number): [Node, number] | number => {
    let left = n;
    for (let i = 0; i < root.childNodes.length; i += 1) {
      const child = root.childNodes[i];
      if (child.nodeType === Node.TEXT_NODE) {
        const len = (child.textContent || '').length;
        if (left <= len) return [child, left];
        left -= len;
      } else if (child instanceof HTMLElement && child.dataset.token !== undefined) {
        if (left === 0) return [root, i];
        left -= 1;
        if (left === 0) return [root, i + 1];
      } else {
        const p = pointAt(child, left);
        if (typeof p !== 'number') return p;
        left = p;
      }
    }
    return left;
  };
  const selectionOffsets = (): SelectionOffsets => {
    const sel = window.getSelection();
    const el = editor.current!;
    if (!sel?.rangeCount) return { start: 0, end: 0 };
    const r = sel.getRangeAt(0);
    if (!el.contains(r.startContainer) || !el.contains(r.endContainer)) return { start: 0, end: 0 };
    return { start: offsetOf(r.startContainer, r.startOffset), end: offsetOf(r.endContainer, r.endOffset) };
  };
  const selectionOffsetsRef = useRef(selectionOffsets);
  selectionOffsetsRef.current = selectionOffsets;
  const restoreOffsets = (offsets: SelectionOffsets) => {
    const el = editor.current!;
    const point = (n: number): [Node, number] => {
      const p = pointAt(el, n);
      return typeof p === 'number' ? [el, el.childNodes.length] : p;
    };
    el.focus();
    const [sn, so] = point(offsets.start);
    const [en, eo] = point(offsets.end);
    const selection = window.getSelection()!;
    selection.setBaseAndExtent(sn, so, en, eo);
    lastRange.current = selection.getRangeAt(0).cloneRange();
    setActive(fmtAt(sn, el));
  };
  const record = (text: string, kind: string) => {
    const now = Date.now();
    const top = history.current[at.current];
    const start = before.current || seen.current || selectionOffsets();
    before.current = null;
    // Continue a typing run only at the caret where the previous input ended.
    const merge = top && at.current > 0 && TYPING.has(kind) && kind === lastEdit.current.kind
      && now - lastEdit.current.time < 1000 && start.start === start.end
      && top.start === start.start && top.end === start.end;
    const undo = merge ? top.undo : start;
    const snap = { text, mode: mode.current, ...selectionOffsets(), undo };
    seen.current = { start: snap.start, end: snap.end };
    if (top && top.text === text && top.mode === snap.mode) {
      if (!TYPING.has(kind)) lastEdit.current = { kind: '', time: 0 };
      return;
    }
    history.current = history.current.slice(0, merge ? at.current : at.current + 1);
    history.current.push(snap);
    if (history.current.length > 200) history.current.shift();
    at.current = history.current.length - 1;
    lastEdit.current = { kind, time: now };
  };
  const travel = (step: number) => {
    const snap = history.current[at.current + step];
    if (!snap || !format) return;
    // Undo puts the caret where the undone edit began; redo where it ended.
    const caret = step < 0 ? history.current[at.current].undo : snap;
    at.current += step;
    before.current = null;
    lastEdit.current = { kind: '', time: 0 };
    mode.current = snap.mode;
    fill(snap.text, snap.mode);
    restoreOffsets(caret);
    seen.current = caret;
    onChangeRef.current(snap.text, snap.mode !== 'plain');
  };
  const travelRef = useRef(travel);
  travelRef.current = travel;

  useEffect(() => {
    const el = editor.current!;
    // StrictMode replays effects. Populate exactly once, not on every keystroke.
    if (!mounted.current) {
      fill(defaultValue, mode.current);
      mounted.current = true;
      if (format) {
        history.current = [{ text: defaultValue, mode: mode.current, start: 0, end: 0, undo: { start: 0, end: 0 } }];
        at.current = 0;
      }
    }
    // Undo/redo from the context menu arrive as beforeinput.
    // The listener is set once: it goes through refs to this render's handlers.
    const onHistory = (ev: InputEvent) => {
      if (!format) return;
      if (ev.inputType !== 'historyUndo' && ev.inputType !== 'historyRedo') {
        // The selection as the edit begins, for the undo of this edit.
        if (!composing.current && !applying.current) before.current = selectionOffsetsRef.current();
        return;
      }
      ev.preventDefault();
      travelRef.current(ev.inputType === 'historyUndo' ? -1 : 1);
    };
    el.addEventListener('beforeinput', onHistory);
    const remember = () => {
      const selection = window.getSelection();
      if (selection?.rangeCount) {
        const range = selection.getRangeAt(0);
        if (el.contains(range.startContainer) && el.contains(range.endContainer)) {
          lastRange.current = range.cloneRange();
          if (format) {
            const offsets = selectionOffsetsRef.current();
            seen.current = offsets;
            const top = history.current[at.current];
            if (top && (offsets.start !== top.start || offsets.end !== top.end)) {
              lastEdit.current = { kind: '', time: 0 };
            }
            setActive(fmtAt(range.startContainer, el));
          }
        }
      }
    };
    document.addEventListener('selectionchange', remember);
    return () => {
      document.removeEventListener('selectionchange', remember);
      el.removeEventListener('beforeinput', onHistory);
      request.current += 1;
    };
  }, []);

  useEffect(() => {
    // The Markdown switch can change the mode of an unformatted block. A block
    // written as one HTML block by this editor stays that way.
    // (After this editor's own change to its HTML block the mode is 'html' and
    // nothing here applies.)
    if (!format || mode.current === 'html') return;
    mode.current = markdown ? 'markdown' : 'plain';
    // The switch is a step of its own: undo and redo give back each state's text with
    // the Markdown setting it had, so tags never come back with Markdown off. (After
    // an undo or redo the prop arrives equal to the state's mode: nothing is added.)
    const top = history.current[at.current];
    if (top && top.mode !== mode.current) {
      const sel = seen.current || { start: 0, end: 0 };
      history.current = history.current.slice(0, at.current + 1);
      history.current.push({ text: top.text, mode: mode.current, ...sel, undo: sel });
      at.current = history.current.length - 1;
      lastEdit.current = { kind: '', time: 0 };
    }
  }, [markdown]);

  const emit = (kind = 'edit', original?: FormatRun[]) => {
    if (!format) { onChange(serialize(editor.current!)); return; }
    // A size span that has got text loses its caret placeholder (only that one
    // character; pasted or stored zero-width spaces stay).
    editor.current!.querySelectorAll<HTMLElement>('[data-axm-caret]').forEach((span) => {
      const first = span.firstChild;
      if (span.textContent === '\u200b') return;
      if (first instanceof Text && first.data.startsWith('\u200b')) first.deleteData(0, 1);
      delete span.dataset.axmCaret;
    });
    const runs = runsOf(editor.current!);
    const safe = original ? format.preserveMarkdownSyntax(original, runs) : runs;
    const res = format.serializeFormatted(safe, mode.current);
    mode.current = format.textMode(res.text, res.markdown);
    if (safe !== runs) {
      const offsets = before.current || selectionOffsets();
      fill(res.text, mode.current);
      restoreOffsets(offsets);
    }
    record(res.text, kind);
    onChange(res.text, res.markdown);
  };

  const pendingSize = useRef<number | null>(null);
  // While a toolbar command runs, the input events of its own execCommand calls are
  // not edits of their own: the command records one state when it is done.
  const applying = useRef(false);
  const convertFonts = () => {
    const fonts = editor.current!.querySelectorAll('font');
    if (!fonts.length) return;
    // Moving the text nodes would drop a caret inside them; put it back.
    const selection = window.getSelection()!;
    const { anchorNode, anchorOffset, focusNode, focusOffset } = selection;
    fonts.forEach((font) => {
      const span = sizeSpan(font.getAttribute('size') === '7' ? pendingSize.current : null);
      font.replaceWith(span);
      span.append(...Array.from(font.childNodes));
    });
    if (anchorNode?.isConnected && focusNode?.isConnected) selection.setBaseAndExtent(anchorNode, anchorOffset, focusNode, focusOffset);
  };
  // Programmatic edits can dispatch input without beforeinput. Capture the live
  // selection first and record once, including any caret adjustment by the command.
  const runEdit = (edit: () => void) => {
    if (format) before.current = selectionOffsets();
    applying.current = true;
    edit();
    applying.current = false;
    if (format) convertFonts();
    emit();
  };

  // Toolbar commands run on the saved selection, so a click on the size menu
  // (which takes focus) still formats what was selected in the box.
  const applyFormat = (command: 'bold' | 'underline' | 'size' | 'clear', size: number | null = null) => {
    const el = editor.current!;
    // A keyboard shortcut can come before the selectionchange that saves the
    // selection: use the live one when it is in the box, the saved one otherwise.
    const live = window.getSelection();
    const liveRange = live?.rangeCount ? live.getRangeAt(0) : null;
    if (document.activeElement === el && liveRange && el.contains(liveRange.startContainer) && el.contains(liveRange.endContainer)) {
      lastRange.current = liveRange.cloneRange();
    } else {
      restore();
    }
    const selection = window.getSelection()!;
    const range = selection.rangeCount ? selection.getRangeAt(0) : null;
    const original = mode.current === 'markdown' ? runsOf(el) : undefined;
    if (original && range?.collapsed
      && format?.isMarkdownSyntaxAt(plainText(original), offsetOf(range.startContainer, range.startOffset, true))) return;
    before.current = selectionOffsets();
    const chipsIn = (r: Range) => Array.from(el.querySelectorAll<HTMLElement>('[data-token]')).filter((c) => r.intersectsNode(c));
    const chips = range && !range.collapsed ? chipsIn(range) : [];
    // The first selected text tells what the command made of the selection.
    const firstText = () => {
      const r = selection.getRangeAt(0);
      const walker = document.createTreeWalker(el, NodeFilter.SHOW_TEXT);
      for (let n = walker.nextNode(); n; n = walker.nextNode()) {
        if (!(n.parentElement?.closest('[data-token]')) && covers(r, n as Text)) return n;
      }
      return null;
    };
    applying.current = true;
    document.execCommand('styleWithCSS', false, 'false');
    if (command === 'bold' || command === 'underline') {
      document.execCommand(command);
      const ref = firstText();
      const value = ref ? fmtAt(ref, el)[command] : !(chips[0] && fmtAt(chips[0], el)[command]);
      chips.forEach((c) => setChipFmt(c, el, command, value));
    } else if (command === 'size' && range?.collapsed) {
      // Nothing selected: what is typed next goes into an empty size span. A
      // zero-width space holds the caret there and is never stored.
      const span = sizeSpan(size);
      span.dataset.axmCaret = '';
      const holder = document.createTextNode('\u200b');
      span.append(holder);
      range.insertNode(span);
      selection.setBaseAndExtent(holder, 1, holder, 1);
    } else if (command === 'size') {
      // Chromium marks the selection with <font size=7> and splits enclosing sizes;
      // swap the marker for this editor's size span.
      pendingSize.current = size;
      document.execCommand('fontSize', false, '7');
      convertFonts();
      chips.forEach((c) => setChipFmt(c, el, 'size', size));
    } else {
      document.execCommand('removeFormat');
      chips.forEach((c) => {
        setChipFmt(c, el, 'size', null);
        setChipFmt(c, el, 'bold', false);
        setChipFmt(c, el, 'underline', false);
      });
    }
    applying.current = false;
    el.normalize();
    if (selection.rangeCount) {
      const r = selection.getRangeAt(0);
      lastRange.current = r.cloneRange();
      const f = { ...fmtAt(r.startContainer, el) };
      // With nothing selected the browser keeps bold/underline for the next typing.
      if (r.collapsed) {
        f.bold = document.queryCommandState('bold');
        f.underline = document.queryCommandState('underline');
      }
      setActive(f);
    }
    emit('format', original);
  };
  const restore = (chip?: HTMLElement) => {
    const el = editor.current!;
    el.focus();
    let range = lastRange.current;
    if (chip?.isConnected) {
      range = document.createRange();
      range.selectNode(chip);
    } else if (!range || !el.contains(range.startContainer) || !el.contains(range.endContainer)) {
      range = document.createRange();
      range.selectNodeContents(el);
      range.collapse(false);
    }
    const selection = window.getSelection()!;
    selection.removeAllRanges();
    selection.addRange(range);
  };
  useEffect(() => {
    if (anchor || !pendingFocus.current) return;
    // The click/Esc handler still runs inside MUI's active focus trap. Restore
    // after its closed render has released the trap, before typing continues.
    const focus = pendingFocus.current;
    pendingFocus.current = null;
    focus();
  }, [anchor]);

  const refresh = async () => {
    const version = ++request.current;
    setLoading(true);
    setItems({ items: config.baseItems, noLists: false, error: false, truncated: false });
    try {
      const result = await config.loadItems();
      if (version === request.current) setItems(result);
    } catch {
      if (version === request.current) setItems({ items: config.baseItems, noLists: false, error: true, truncated: false });
    } finally {
      if (version === request.current) setLoading(false);
    }
  };
  const open = (target: HTMLElement, chip: HTMLElement | null = null) => {
    if (config.disabled) return;
    const part = chip && config.parseTokens(chip.dataset.token || '').find((p) => p.type === 'token');
    setEditing(chip);
    setSelected(part && part.type === 'token' ? part.item : null);
    setFallback(part && part.type === 'token' ? part.fallback : '');
    setSearch('');
    setAnchor(target);
    refresh();
  };
  const close = (focus: () => void) => {
    pendingFocus.current = focus;
    setAnchor(null);
    request.current += 1;
  };
  const cancel = () => {
    close(() => {
      if (editing?.isConnected) editing.focus(); else button.current?.focus();
    });
  };
  const leaveChip = () => {
    // Chromium may leave the caret inside a noneditable span after insertHTML.
    // Place it immediately after the inserted chip, without adding stored text.
    const selection = window.getSelection()!;
    const node = selection.anchorNode;
    const inserted = node instanceof Element ? node.closest('[data-token]') : node?.parentElement?.closest('[data-token]');
    if (inserted) {
      const range = document.createRange();
      range.setStartAfter(inserted); range.collapse(true);
      selection.removeAllRanges(); selection.addRange(range);
    }
  };
  const insertMarkup = (html: string) => {
    document.execCommand('insertHTML', false, html);
    leaveChip();
  };
  const apply = (remove = false) => {
    if (!remove && !selected) return;
    restore(editing || undefined);
    runEdit(() => {
      if (remove) document.execCommand('delete');
      else {
        const chip = makeChip(config, selected!, fallback, config.buildToken(selected!, fallback));
        insertMarkup(chip.outerHTML);
      }
    });
    lastRange.current = window.getSelection()?.getRangeAt(0).cloneRange() || null;
    close(() => restore());
  };
  const copy = (ev: React.ClipboardEvent, cut = false) => {
    const selection = window.getSelection();
    if (selection?.rangeCount && !selection.isCollapsed) {
      ev.preventDefault();
      ev.clipboardData.setData('text/plain', serialize(selection.getRangeAt(0).cloneContents()));
      if (cut) runEdit(() => document.execCommand('delete'));
    }
  };
  const chipFrom = (target: EventTarget) => (target instanceof Element ? target.closest<HTMLElement>('[data-token]') : null);
  const query = search.trim().toLocaleLowerCase();
  const filtered = items.items.filter((item) => `${config.getLabel(item)} ${item.key || ''}`.toLocaleLowerCase().includes(query));
  const renderGroup = (base: boolean) => {
    const group = filtered.filter((item) => Boolean(item.expression) === base);
    if (!group.length) return null;
    return <React.Fragment key={String(base)}>
      <Typography variant="caption" color="text.secondary">{base ? s.base : s.attributes}</Typography>
      <List dense disablePadding aria-label={base ? s.base : s.attributes}>
        {group.map((item) => <ListItemButton key={item.id} selected={selected?.id === item.id}
          onClick={() => setSelected(item)} sx={{ px: 1, py: 0.75, gap: 1, overflowWrap: 'anywhere' }}>
          <Box flex={1}>{config.getLabel(item)}</Box>
          {item.key && config.getLabel(item) !== item.key && <Typography variant="caption" color="text.secondary">{item.key}</Typography>}
          {selected?.id === item.id && <span aria-hidden="true">✓</span>}
        </ListItemButton>)}
      </List>
    </React.Fragment>;
  };

  return <Box>
    <Stack direction="row" justifyContent="space-between" alignItems="center" mb={1} gap={1}>
      <Typography id={`${id}-label`} variant="body1">{label}</Typography>
      <Button ref={button} disabled={config.disabled} size="small" variant="outlined" startIcon={<Add />} aria-haspopup="dialog"
        aria-expanded={Boolean(anchor)} onMouseDown={(ev) => ev.preventDefault()} onClick={() => open(button.current!)}>{s.insert}</Button>
    </Stack>
    {format && <Stack direction="row" alignItems="center" gap={0.5} mb={1} role="toolbar" aria-label={format.strings.toolbar}>
      <Tooltip title={`${format.strings.bold} (Ctrl+B)`}>
        <span><ToggleButton value="bold" size="small" selected={active.bold} disabled={config.disabled}
          aria-label={format.strings.bold} data-format="bold" sx={{ p: 0.5 }}
          onMouseDown={(ev) => ev.preventDefault()} onClick={() => applyFormat('bold')}>
          <FormatBold fontSize="small" />
        </ToggleButton></span>
      </Tooltip>
      <Tooltip title={`${format.strings.underline} (Ctrl+U)`}>
        <span><ToggleButton value="underline" size="small" selected={active.underline} disabled={config.disabled}
          aria-label={format.strings.underline} data-format="underline" sx={{ p: 0.5 }}
          onMouseDown={(ev) => ev.preventDefault()} onClick={() => applyFormat('underline')}>
          <FormatUnderlined fontSize="small" />
        </ToggleButton></span>
      </Tooltip>
      <Select size="small" displayEmpty value={active.size ?? ''} disabled={config.disabled}
        inputProps={{ 'aria-label': format.strings.size, 'data-format': 'size' }}
        sx={{ minWidth: 104, fontSize: 13, '& .MuiSelect-select': { py: 0.6 } }}
        renderValue={(v) => (v === '' ? format.strings.size : `${v}px`)}
        MenuProps={{ disableRestoreFocus: true, TransitionProps: { onExited: () => restore() } }}>
        {/* A menu item applies on every activation, even when its value is already displayed. */}
        <MenuItem value="" onClick={() => applyFormat('size')}>{format.strings.sizeDefault}</MenuItem>
        {format.sizes.map((s) => <MenuItem key={s} value={s} sx={{ fontSize: Math.min(s, 24) }}
          onClick={() => applyFormat('size', s)}>{s}px</MenuItem>)}
      </Select>
      <Tooltip title={format.strings.clear}>
        <span><IconButton size="small" aria-label={format.strings.clear} data-format="clear" disabled={config.disabled}
          onMouseDown={(ev) => ev.preventDefault()} onClick={() => applyFormat('clear')}>
          <FormatClear fontSize="small" />
        </IconButton></span>
      </Tooltip>
    </Stack>}
    <Box ref={editor} contentEditable={!config.disabled} suppressContentEditableWarning role="textbox" aria-multiline="true"
      aria-labelledby={`${id}-label`} tabIndex={0}
      sx={{ whiteSpace: 'pre-wrap', overflowWrap: 'anywhere', minHeight: `${rows * 1.65 + 1.5}em`,
        p: 1.5, border: '1px solid #aaa', borderRadius: 1, fontSize: 14, lineHeight: 1.65,
        '&:focus': { outline: '2px solid #1769aa', outlineOffset: -1 }, '& [data-token]:focus': { outline: '2px solid #1769aa' } }}
      onCompositionStart={() => {
        composing.current = true;
        if (format) before.current = selectionOffsets();
      }}
      onCompositionEnd={() => { composing.current = false; if (format) convertFonts(); emit(); }}
      onInput={(ev) => {
        if (!composing.current && !applying.current) {
          if (format) convertFonts();
          emit((ev.nativeEvent as InputEvent).inputType);
        }
      }}
      onClick={(ev) => { const chip = chipFrom(ev.target); if (chip) open(chip, chip); }}
      onKeyDown={(ev) => {
        if (composing.current || ev.nativeEvent.isComposing) return;
        // Native Ctrl+B/I/U formatting would show in this box but never reach the
        // stored text. Keep only what this box can store.
        if (format && (ev.ctrlKey || ev.metaKey) && !ev.altKey && /^[zy]$/i.test(ev.key)) {
          ev.preventDefault();
          travel(ev.key.toLowerCase() === 'y' || ev.shiftKey ? 1 : -1);
          return;
        }
        if ((ev.ctrlKey || ev.metaKey) && !ev.altKey && /^[biu]$/i.test(ev.key)) {
          ev.preventDefault();
          if (format && !config.disabled && /^[bu]$/i.test(ev.key)) applyFormat(ev.key.toLowerCase() === 'b' ? 'bold' : 'underline');
          return;
        }
        const chip = chipFrom(ev.target);
        if (chip && (ev.key === 'Enter' || ev.key === ' ')) { ev.preventDefault(); open(chip, chip); }
        else if (chip && (ev.key === 'Backspace' || ev.key === 'Delete')) {
          ev.preventDefault();
          restore(chip);
          runEdit(() => document.execCommand('delete'));
        } else if (ev.key === 'Enter') {
          ev.preventDefault();
          runEdit(() => document.execCommand('insertLineBreak'));
        }
      }}
      onPaste={(ev) => {
        ev.preventDefault();
        const text = ev.clipboardData.getData('text/plain').replace(/\r\n?/g, '\n');
        const parts = config.parseTokens(text);
        // Insert escaped text as HTML so line breaks stay text nodes. Native
        // insertText creates DIVs, and Chromium moves a trailing noneditable
        // chip out of such a DIV, adding an unintended line break.
        const content = document.createElement('div');
        parts.forEach((part) => content.append(part.type === 'text' ? document.createTextNode(part.value)
          : makeChip(config, part.item, part.fallback, part.raw)));
        runEdit(() => insertMarkup(content.innerHTML));
      }}
      onDrop={(ev) => ev.preventDefault()}
      onCopy={(ev) => copy(ev)}
      onCut={(ev) => copy(ev, true)}
    />
    <Popover open={Boolean(anchor)} anchorEl={anchor} onClose={cancel} disableRestoreFocus
      anchorOrigin={{ vertical: 'bottom', horizontal: 'right' }} transformOrigin={{ vertical: 'top', horizontal: 'right' }}
      PaperProps={{ role: 'dialog', 'aria-label': editing ? s.edit : s.insert,
        sx: { width: 336, maxWidth: 'calc(100% - 24px)', maxHeight: 'calc(100% - 24px)', borderRadius: 2, p: 2 } }}>
      <Typography fontWeight={600} mb={1}>{editing ? s.edit : s.insert}</Typography>
      <TextField autoFocus fullWidth size="small" label={s.search} value={search} onChange={(ev) => setSearch(ev.target.value)} sx={{ mb: 1 }} />
      <Box sx={{ maxHeight: 220, overflowY: 'auto' }}>{renderGroup(true)}{renderGroup(false)}</Box>
      {!filtered.length && !loading && <Typography variant="body2" my={1}>{s.noResults}</Typography>}
      <Box role="status" my={1}>
        {loading ? <Stack direction="row" gap={1}><CircularProgress size={16} /><Typography variant="caption">{s.loading}</Typography></Stack>
          : <Typography variant="caption" color="text.secondary">
            {items.error ? s.error : items.noLists ? s.noLists : s.sample}
            {items.truncated && <> {s.truncated}</>}
          </Typography>}
      </Box>
      <Button size="small" startIcon={<Refresh />} disabled={loading} onClick={refresh} sx={{ mb: 2 }}>{s.refresh}</Button>
      <TextField fullWidth size="small" label={s.fallback} value={fallback} onChange={(ev) => setFallback(ev.target.value)} helperText={s.fallbackHelp}
        onKeyDown={(ev) => {
          if (ev.key === 'Enter' && !ev.nativeEvent.isComposing && selected) {
            ev.preventDefault(); apply();
          }
        }} />
      <Stack direction="row" gap={1} justifyContent="flex-end" sx={{ pt: 2, position: 'sticky', bottom: -16, bgcolor: 'background.paper', pb: 1 }}>
        {editing && <Button color="error" onClick={() => apply(true)} sx={{ mr: 'auto' }}>{s.remove}</Button>}
        <Button onClick={cancel}>{s.cancel}</Button>
        <Button variant="contained" disabled={!selected} onClick={() => apply()}>{editing ? s.apply : s.add}</Button>
      </Stack>
    </Popover>
  </Box>;
}
