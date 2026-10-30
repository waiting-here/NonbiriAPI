import type { ReactNode } from 'react';

/**
 * Responsive form grid. Columns follow the container width, so the same form reads as one
 * column in a drawer or phone and as several columns on a wide page. Children are usually
 * `Field`s; give long fields (URLs, descriptions) `size="long"` and short ones
 * (numbers, IDs, states) `size="short"`.
 */
export function Fields({
  legend,
  hint,
  children,
  className,
}: {
  legend?: ReactNode;
  hint?: ReactNode;
  children: ReactNode;
  className?: string;
}) {
  const grid = <div className="nb-fields__grid">{children}</div>;
  if (!legend) return <div className={`nb-fields${className ? ` ${className}` : ''}`}>{grid}</div>;
  return (
    <fieldset className={`nb-fields${className ? ` ${className}` : ''}`}>
      <legend className="nb-fields__legend">{legend}</legend>
      {hint ? <p className="nb-fields__hint">{hint}</p> : null}
      {grid}
    </fieldset>
  );
}

/** Bottom action row of a form section: one primary, aligned to the end of the section. */
export function FormActions({
  children,
  note,
  align = 'end',
}: {
  children: ReactNode;
  note?: ReactNode;
  align?: 'start' | 'end';
}) {
  return (
    <div className={`nb-form-actions nb-form-actions--${align}`}>
      {note ? <div className="nb-form-actions__note">{note}</div> : null}
      <div className="nb-form-actions__buttons">{children}</div>
    </div>
  );
}
