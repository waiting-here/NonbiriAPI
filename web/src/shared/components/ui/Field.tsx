import { useId, type ReactNode } from 'react';

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
  const helpId = help ? `${id}-help` : undefined;
  const errorId = error ? `${id}-error` : undefined;
  const describedBy = [errorId, helpId].filter(Boolean).join(' ') || undefined;
  return (
    <div className={`nb-field${span ? ' nb-field--span' : ''}`}>
      <label className="nb-field__label" htmlFor={id}>
        {label}
        {optional ? (
          <>
            {' '}
            <span className="nb-field__tag">{optional}</span>
          </>
        ) : null}
      </label>
      {children({ id, 'aria-describedby': describedBy, 'aria-invalid': error ? true : undefined })}
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
    </div>
  );
}
