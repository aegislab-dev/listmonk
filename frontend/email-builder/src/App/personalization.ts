import { create } from 'zustand';

export type PersonalizationItem = { id: string; key?: string; expression?: string; label?: string };
export type PersonalizationPart = { type: 'text'; value: string } | {
  type: 'token'; raw: string; item: PersonalizationItem; fallback: string;
};
export type ItemResult = {
  items: PersonalizationItem[]; noLists: boolean; error: boolean; truncated: boolean;
};
export type TextMode = 'plain' | 'markdown' | 'html';
export type FormatSegment = { value: string; bold: boolean; underline: boolean; size: number | null };
export type FormatRun = ({ type: 'text'; value: string } | { type: 'token'; raw: string })
  & { bold?: boolean; underline?: boolean; size?: number | null };
// Inline formatting of Text blocks (LST-77). The pure functions live in
// frontend/src/text-format.js so they are unit-tested with the main frontend.
export type Formatting = {
  strings: Record<string, string>;
  sizes: number[];
  textMode: (text: string, markdown?: boolean | null) => TextMode;
  parseFormatted: (text: string, mode: TextMode) => FormatSegment[];
  isMarkdownSyntaxAt: (text: string, offset: number) => boolean;
  preserveMarkdownSyntax: (before: FormatRun[], after: FormatRun[]) => {
    runs: FormatRun[];
    insertions: { offset: number; text: string }[];
  };
  serializeFormatted: (runs: FormatRun[], mode: TextMode) => { text: string; markdown: boolean };
};
export type Personalization = {
  format?: Formatting;
  disabled?: boolean;
  strings: Record<string, string>;
  baseItems: PersonalizationItem[];
  loadItems: () => Promise<ItemResult>;
  buildToken: (item: PersonalizationItem, fallback: string) => string;
  parseTokens: (text: string) => PersonalizationPart[];
  getLabel: (item: PersonalizationItem) => string;
};
const store = create<{ config?: Personalization }>(() => ({}));
export const setPersonalization = (config?: Personalization) => store.setState({ config });
export const usePersonalization = () => store((s) => s.config);
export const chipLabel = (config: Personalization, item: PersonalizationItem, fallback: string) => (
  `${config.getLabel(item)}${fallback ? ` · ${config.strings.ifEmpty} “${fallback}”` : ''}`
);
export const chipStyle = {
  display: 'inline', background: '#e8f2ff', color: '#175b99',
  border: '1px solid #9cc7ee', borderRadius: '5px', padding: '1px 5px',
  fontSize: '0.9em', overflowWrap: 'anywhere',
} as const;
