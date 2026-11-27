import React from 'react';

import { FormControlLabel, Switch } from '@mui/material';

type Props = {
  label: string;
  defaultValue: boolean;
  disabled?: boolean;
  onChange: (value: boolean) => void;
};

export default function BooleanInput({ label, defaultValue, disabled, onChange }: Props) {
  return (
    <FormControlLabel
      label={label}
      control={
        <Switch
          checked={defaultValue}
          disabled={disabled}
          onChange={(_, checked: boolean) => {
            onChange(checked);
          }}
        />
      }
    />
  );
}
