import { useId, type ReactNode } from 'react';
import { RadioGroup, Radio } from 'react-aria-components';

export function OptionCards<V extends string>({
  legend,
  value,
  options,
  onChange,
}: {
  legend: ReactNode;
  value: V;
  options: readonly { value: V; title: ReactNode; body?: ReactNode; disabled?: boolean }[];
  onChange: (next: V) => void;
}) {
  const id = useId();
  return (
    <fieldset className="nb-fieldset">
      <legend id={id}>{legend}</legend>
      <RadioGroup
        className="nb-options"
        aria-labelledby={id}
        value={value}
        onChange={(next) => onChange(next as V)}
      >
        {options.map((option) => (
          <Radio
            key={option.value}
            value={option.value}
            className="nb-option"
            isDisabled={option.disabled}
            aria-labelledby={id + '-' + option.value + '-title'}
            aria-describedby={option.body ? id + '-' + option.value + '-body' : undefined}
          >
            <strong id={id + '-' + option.value + '-title'}>{option.title}</strong>
            {option.body ? <span id={id + '-' + option.value + '-body'}>{option.body}</span> : null}
          </Radio>
        ))}
      </RadioGroup>
    </fieldset>
  );
}
