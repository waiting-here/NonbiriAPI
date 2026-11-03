import { FieldLabelContext } from './FieldLayoutContext';
import { useEffect, useContext, useRef, type InputHTMLAttributes, type RefObject } from 'react';
import { ComboBox, ComboBoxStateContext, Input, ListBox, ListBoxItem } from 'react-aria-components';
import { AnchoredSurface } from '../OverlaySurface';

type Props = Omit<InputHTMLAttributes<HTMLInputElement>, 'value' | 'onChange' | 'list'> & {
  value: string;
  suggestions: readonly string[];
  onChange: (value: string) => void;
};

function Suggestions({
  triggerRef,
  disabled,
}: {
  triggerRef: RefObject<HTMLInputElement | null>;
  disabled: boolean;
}) {
  const state = useContext(ComboBoxStateContext);
  useEffect(() => {
    if (disabled && state?.isOpen) state.close();
  }, [disabled, state]);
  const list = (
    <ListBox className="nb-suggestions-list">
      {(item: { id: string }) => (
        <ListBoxItem id={item.id} textValue={item.id}>
          {item.id}
        </ListBoxItem>
      )}
    </ListBox>
  );
  if (!state) return list;
  return (
    <AnchoredSurface
      open={state.isOpen && !disabled}
      onClose={state.close}
      triggerRef={triggerRef}
      nonModal
      className="nb-suggestions-popover"
    >
      {list}
    </AnchoredSurface>
  );
}

export function SuggestInput({
  value,
  onChange,
  suggestions,
  disabled,
  readOnly,
  required,
  className,
  'aria-label': label,
  'aria-labelledby': labelledBy,
  'aria-describedby': describedBy,
  'aria-invalid': invalid,
  ...input
}: Props) {
  const ref = useRef<HTMLInputElement>(null);
  const fieldLabel = useContext(FieldLabelContext);
  return (
    <ComboBox
      className="nb-suggestions"
      inputValue={value}
      onInputChange={(next) => {
        if (!disabled && !readOnly) onChange(next);
      }}
      items={[...new Set(suggestions)]
        .filter((item) => item.toLocaleLowerCase().includes(value.toLocaleLowerCase()))
        .map((id) => ({ id }))}
      allowsCustomValue
      menuTrigger="focus"
      isDisabled={disabled}
      isReadOnly={readOnly}
      isRequired={required}
      isInvalid={invalid === true || invalid === 'true'}
      validationBehavior="aria"
      aria-label={label}
      aria-labelledby={label ? undefined : (labelledBy ?? fieldLabel)}
      aria-describedby={describedBy}
    >
      <Input {...input} ref={ref} className={className} />
      <Suggestions triggerRef={ref} disabled={Boolean(disabled || readOnly)} />
    </ComboBox>
  );
}
