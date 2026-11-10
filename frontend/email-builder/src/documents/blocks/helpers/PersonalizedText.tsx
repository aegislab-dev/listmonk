import React from 'react';
import { Heading, HeadingProps } from '@usewaypoint/block-heading';
import { Text, TextProps } from '@usewaypoint/block-text';
import { chipLabel, chipStyle, usePersonalization } from '../../../App/personalization';

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
    return <Text {...data} props={{ ...data.props, text: parts.map((p) => p.type === 'text' ? p.value
      : `〔${chipLabel(config, p.item, p.fallback)}〕`).join('') }} />;
  }
  return React.cloneElement(element, {}, parts.map((part, index) => part.type === 'text' ? part.value
    : <span key={index} style={chipStyle}>{chipLabel(config, part.item, part.fallback)}</span>));
}
