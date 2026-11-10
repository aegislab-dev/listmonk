import { create } from 'zustand';

export type PersonalizationItem = { id: string; key?: string; expression?: string; label?: string };
export type PersonalizationPart = { type: 'text'; value: string } | {
  type: 'token'; raw: string; item: PersonalizationItem; fallback: string;
};
export type ItemResult = {
  items: PersonalizationItem[]; noLists: boolean; error: boolean; truncated: boolean;
};
export type Personalization = {
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
