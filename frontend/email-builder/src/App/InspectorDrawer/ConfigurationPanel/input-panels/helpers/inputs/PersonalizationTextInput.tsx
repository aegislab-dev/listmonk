import React, { useEffect, useId, useRef, useState } from 'react';
import { Add, Refresh } from '@mui/icons-material';
import { Box, Button, CircularProgress, List, ListItemButton, Popover, Stack, TextField, Typography } from '@mui/material';
import {
  chipLabel, chipStyle, ItemResult, Personalization, PersonalizationItem,
} from '../../../../../personalization';

type Props = {
  config: Personalization;
  label: string;
  rows: number;
  defaultValue: string;
  onChange: (text: string) => void;
};

// This DOM is owned by the browser while editing so React never resets an IME
// composition, selection or native undo transaction on a document update.
function serialize(node: Node): string {
  if (node.nodeType === Node.TEXT_NODE) return node.textContent || '';
  if (node instanceof HTMLElement) {
    if (node.dataset.token !== undefined) return node.dataset.token;
    if (node.tagName === 'BR') return '\n';
  }
  // Chromium also creates block wrappers when insertText contains a newline.
  // A lone <br> is an empty-line caret placeholder, not an extra stored newline.
  if (node instanceof HTMLElement && node.childNodes.length === 1 && node.firstChild?.nodeName === 'BR') return '';
  let text = '';
  Array.from(node.childNodes).forEach((child, index, children) => {
    const isBlock = (n: Node) => n instanceof HTMLElement && /^(DIV|P)$/.test(n.tagName);
    if (isBlock(child) && index > 0) text += '\n';
    text += serialize(child);
    if (isBlock(child) && children[index + 1] && !isBlock(children[index + 1])) text += '\n';
  });
  return text;
}

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

export default function PersonalizationTextInput({ config, label, rows, defaultValue, onChange }: Props) {
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

  useEffect(() => {
    const el = editor.current!;
    // StrictMode replays effects. Populate exactly once, not on every keystroke.
    if (!mounted.current) {
      for (const part of config.parseTokens(defaultValue)) {
        el.append(part.type === 'text' ? document.createTextNode(part.value)
          : makeChip(config, part.item, part.fallback, part.raw));
      }
      mounted.current = true;
    }
    const remember = () => {
      const selection = window.getSelection();
      if (selection?.rangeCount) {
        const range = selection.getRangeAt(0);
        if (el.contains(range.startContainer) && el.contains(range.endContainer)) {
          lastRange.current = range.cloneRange();
        }
      }
    };
    document.addEventListener('selectionchange', remember);
    return () => { document.removeEventListener('selectionchange', remember); request.current += 1; };
  }, []);

  const emit = () => onChange(serialize(editor.current!));
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
    if (remove) {
      document.execCommand('delete');
    } else {
      const chip = makeChip(config, selected!, fallback, config.buildToken(selected!, fallback));
      insertMarkup(chip.outerHTML);
    }
    lastRange.current = window.getSelection()?.getRangeAt(0).cloneRange() || null;
    emit();
    close(() => restore());
  };
  const copy = (ev: React.ClipboardEvent, cut = false) => {
    const selection = window.getSelection();
    if (selection?.rangeCount && !selection.isCollapsed) {
      ev.preventDefault();
      ev.clipboardData.setData('text/plain', serialize(selection.getRangeAt(0).cloneContents()));
      if (cut) { document.execCommand('delete'); emit(); }
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
    <Box ref={editor} contentEditable={!config.disabled} suppressContentEditableWarning role="textbox" aria-multiline="true"
      aria-labelledby={`${id}-label`} tabIndex={0}
      sx={{ whiteSpace: 'pre-wrap', overflowWrap: 'anywhere', minHeight: `${rows * 1.65 + 1.5}em`,
        p: 1.5, border: '1px solid #aaa', borderRadius: 1, fontSize: 14, lineHeight: 1.65,
        '&:focus': { outline: '2px solid #1769aa', outlineOffset: -1 }, '& [data-token]:focus': { outline: '2px solid #1769aa' } }}
      onCompositionStart={() => { composing.current = true; }}
      onCompositionEnd={() => { composing.current = false; emit(); }}
      onInput={() => { if (!composing.current) emit(); }}
      onClick={(ev) => { const chip = chipFrom(ev.target); if (chip) open(chip, chip); }}
      onKeyDown={(ev) => {
        if (composing.current || ev.nativeEvent.isComposing) return;
        const chip = chipFrom(ev.target);
        if (chip && (ev.key === 'Enter' || ev.key === ' ')) { ev.preventDefault(); open(chip, chip); }
        else if (chip && (ev.key === 'Backspace' || ev.key === 'Delete')) {
          ev.preventDefault(); restore(chip); document.execCommand('delete'); emit();
        } else if (ev.key === 'Enter') { ev.preventDefault(); document.execCommand('insertLineBreak'); }
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
        insertMarkup(content.innerHTML);
        emit();
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
