import React, { useState } from 'react';

import { Typography } from '@mui/material';
import { TextProps, TextPropsSchema } from '@usewaypoint/block-text';

import BaseSidebarPanel from './helpers/BaseSidebarPanel';
import BooleanInput from './helpers/inputs/BooleanInput';
import TextInput from './helpers/inputs/TextInput';
import PersonalizationTextInput from './helpers/inputs/PersonalizationTextInput';
import { usePersonalization } from '../../../personalization';
import MultiStylePropertyPanel from './helpers/style-inputs/MultiStylePropertyPanel';

type TextSidebarPanelProps = {
  data: TextProps;
  setData: (v: TextProps) => void;
};
export default function TextSidebarPanel({ data, setData }: TextSidebarPanelProps) {
  const personalization = usePersonalization();
  const format = personalization?.format;
  const text = data.props?.text ?? '';
  const mode = format ? format.textMode(text, data.props?.markdown) : null;
  // Formatting is inline HTML in Markdown: turning Markdown off would print the tags.
  const formatted = Boolean(format && mode === 'markdown' && format.parseFormatted(text, mode).some(
    (s) => s.bold || s.underline || s.size,
  ));
  const [, setErrors] = useState<Zod.ZodError | null>(null);

  const updateData = (d: unknown) => {
    const res = TextPropsSchema.safeParse(d);
    if (res.success) {
      setData(res.data);
      setErrors(null);
    } else {
      setErrors(res.error);
    }
  };

  return (
    <BaseSidebarPanel title="Text block">
      {personalization ? (
        <PersonalizationTextInput
          config={personalization}
          label="Content"
          rows={5}
          formatting
          markdown={data.props?.markdown}
          defaultValue={data.props?.text ?? ''}
          onChange={(text, markdown) => updateData({
            ...data, props: { ...data.props, text, ...(markdown === undefined ? {} : { markdown }) },
          })}
        />
      ) : (
        <TextInput
          label="Content"
          rows={5}
          defaultValue={data.props?.text ?? ''}
          onChange={(text) => updateData({ ...data, props: { ...data.props, text } })}
        />
      )}
      {mode !== 'html' && (
        <BooleanInput
          label="Markdown"
          disabled={formatted}
          defaultValue={data.props?.markdown ?? false}
          onChange={(markdown) => updateData({ ...data, props: { ...data.props, markdown } })}
        />
      )}
      {formatted && format && (
        <Typography variant="caption" color="text.secondary">{format.strings.markdownLocked}</Typography>
      )}

      <MultiStylePropertyPanel
        names={['color', 'backgroundColor', 'fontFamily', 'fontSize', 'fontWeight', 'textAlign', 'padding']}
        value={data.style}
        onChange={(style) => updateData({ ...data, style })}
      />
    </BaseSidebarPanel>
  );
}
