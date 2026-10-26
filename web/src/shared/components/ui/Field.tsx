import { useContext, useId, type ReactNode } from 'react';
import { FilterFieldLayoutContext } from './FieldLayoutContext';

export function Field({
  label,
  optional,
  help,
  error,
  span,
  children,
}: {
  label: ReactNode;
  optional?: ReactNode;
  help?: ReactNode;
  error?: ReactNode;
  span?: boolean;
  children: (props: {
    id: string;
    'aria-describedby'?: string;
    'aria-invalid'?: boolean;
  }) => ReactNode;
}) {
  const id = useId();
  const filterLayout = useContext(FilterFieldLayoutContext);
  const helpId = help ? `${id}-help` : undefined;
  const errorId = error ? `${id}-error` : undefined;
  const describedBy = [errorId, helpId].filter(Boolean).join(' ') || undefined;
  const control = children({
    id,
    'aria-describedby': describedBy,
    'aria-invalid': error ? true : undefined,
  });
  const messages = (
    <>
      {error ? (
        <p id={errorId} className="nb-field__error" role="alert">
          {error}
        </p>
      ) : null}
      {help ? (
        <p id={helpId} className="nb-field__help">
          {help}
        </p>
      ) : null}
    </>
  );
  return (
    <div
      className={`nb-field${span ? ' nb-field--span' : ''}${filterLayout ? ' nb-field--filter' : ''}`}
    >
      <label className="nb-field__label" htmlFor={id}>
        {label}
        {optional ? (
          <>
            {' '}
            <span className="nb-field__tag">{optional}</span>
          </>
        ) : null}
      </label>
      {filterLayout ? <div className="nb-field__control">{control}</div> : control}
      {filterLayout ? <div className="nb-field__messages">{messages}</div> : messages}
    </div>
  );
}
