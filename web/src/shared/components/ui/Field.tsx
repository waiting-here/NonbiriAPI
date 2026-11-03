import { useField } from 'react-aria';
import { useContext, useId, type ReactNode } from 'react';
import { FieldLabelContext, FilterFieldLayoutContext } from './FieldLayoutContext';

export function Field({
  label,
  optional,
  help,
  error,
  span,
  size,
  children,
}: {
  label: ReactNode;
  optional?: ReactNode;
  help?: ReactNode;
  error?: ReactNode;
  /** Full row of the form grid. */
  span?: boolean;
  /** Width tier inside `Fields`: short (numbers, IDs, states), medium (default), long (URLs, text). */
  size?: 'short' | 'medium' | 'long';
  children: (props: {
    id: string;
    'aria-labelledby'?: string;
    'aria-describedby'?: string;
    'aria-invalid'?: boolean;
  }) => ReactNode;
}) {
  const id = useId();
  const filterLayout = useContext(FilterFieldLayoutContext);
  const { labelProps, fieldProps, descriptionProps, errorMessageProps } = useField({
    id,
    label,
    description: help,
    errorMessage: error,
    isInvalid: Boolean(error),
  });
  const control = children({
    ...fieldProps,
    // Native labels associate through htmlFor; allow a consumer's explicit aria-label to win.
    'aria-labelledby': undefined,
    id,
    'aria-invalid': error ? true : undefined,
    'aria-describedby':
      [error ? errorMessageProps.id : undefined, help ? descriptionProps.id : undefined]
        .filter(Boolean)
        .join(' ') || undefined,
  });
  const messages = (
    <>
      {error ? (
        <p {...errorMessageProps} className="nb-field__error" role="alert">
          {error}
        </p>
      ) : null}
      {help ? (
        <p {...descriptionProps} className="nb-field__help">
          {help}
        </p>
      ) : null}
    </>
  );
  return (
    <div
      className={`nb-field${span ? ' nb-field--span' : ''}${size ? ` nb-field--${size}` : ''}${filterLayout ? ' nb-field--filter' : ''}`}
    >
      <label {...labelProps} className="nb-field__label">
        {label}
        {optional ? (
          <>
            {' '}
            <span className="nb-field__tag">{optional}</span>
          </>
        ) : null}
      </label>
      <FieldLabelContext.Provider value={labelProps.id}>
        {filterLayout ? <div className="nb-field__control">{control}</div> : control}
      </FieldLabelContext.Provider>
      {filterLayout ? <div className="nb-field__messages">{messages}</div> : messages}
    </div>
  );
}
