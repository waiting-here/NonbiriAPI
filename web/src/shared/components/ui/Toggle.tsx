import { useId, type ReactNode } from 'react';
import { Switch } from 'react-aria-components';

export function Toggle({
  label,
  description,
  checked,
  disabled,
  onChange,
}: {
  label: ReactNode;
  description?: ReactNode;
  checked: boolean;
  disabled?: boolean;
  onChange: (next: boolean) => void;
}) {
  const id = useId();
  return (
    <Switch
      className="nb-toggle"
      isSelected={checked}
      isDisabled={disabled}
      onChange={onChange}
      aria-labelledby={id}
      aria-describedby={description ? id + '-description' : undefined}
    >
      <span className="nb-toggle__text">
        <strong id={id}>{label}</strong>
        {description ? <span id={id + '-description'}>{description}</span> : null}
      </span>
      <span className="nb-toggle__indicator" aria-hidden="true" />
    </Switch>
  );
}
