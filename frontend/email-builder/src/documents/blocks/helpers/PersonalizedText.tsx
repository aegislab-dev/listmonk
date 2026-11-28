import React from 'react';
import { Heading, HeadingProps } from '@usewaypoint/block-heading';
import { Text, TextProps } from '@usewaypoint/block-text';
import { chipLabel, chipStyle, usePersonalization } from '../../../App/personalization';

const escapeHtml = (s: string) => s.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;').replace(/"/g, '&quot;');
const kebab = (k: string) => k.replace(/[A-Z]/g, (c) => `-${c.toLowerCase()}`);
const chipCss = Object.entries(chipStyle).map(([k, v]) => `${kebab(k)}:${v}`).join(';');

// Reuse the upstream element and styles; replace only its editor-side children.
// The saved email uses the separate, unchanged static renderer.
export default function PersonalizedText({ heading = false, ...data }: TextProps & HeadingProps & { heading?: boolean }) {
  const config = usePersonalization();
  const text = data.props?.text || '';
  const element = heading ? Heading(data) : Text(data);
  if (!config) return element;
  const parts = config.parseTokens(text);
  if (!parts.some((p) => p.type === 'token')) return element;
  if (!heading && data.props?.markdown) {
    // Markdown (and formatted text, which is inline HTML in Markdown, LST-77): show a
    // token as a chip span so it keeps the formatting around it. Inside an HTML tag
    // (an attribute such as href) a span would break the markup, so it stays text.
    // The same goes for Markdown syntax that is not display text: inline code, a link
    // destination or title, an autolink or an image.
    let before = '';
    let offset = 0;
    const shown = parts.map((p) => {
      let out: string;
      const at = offset;
      offset += p.type === 'text' ? p.value.length : p.raw.length;
      if (p.type === 'text') out = p.value;
      else if (before.lastIndexOf('<') > before.lastIndexOf('>')
        || config.format?.isMarkdownSyntaxAt(text, at)) out = `〔${chipLabel(config, p.item, p.fallback)}〕`;
      else out = `<span style="${escapeHtml(chipCss)}">${escapeHtml(chipLabel(config, p.item, p.fallback))}</span>`;
      before += out;
      return out;
    }).join('');
    return <Text {...data} props={{ ...data.props, text: shown }} />;
  }
  return React.cloneElement(element, {}, parts.map((part, index) => part.type === 'text' ? part.value
    : <span key={index} style={chipStyle}>{chipLabel(config, part.item, part.fallback)}</span>));
}
