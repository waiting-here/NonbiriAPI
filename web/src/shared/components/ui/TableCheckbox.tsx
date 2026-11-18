import type { InputHTMLAttributes } from 'react';

type Props = Pick<
  InputHTMLAttributes<HTMLInputElement>,
  'aria-label' | 'checked' | 'disabled' | 'onChange' | 'title'
> & {
  indeterminate?: boolean;
  text?: string;
};

export function TableCheckbox({ indeterminate = false, text, title, ...input }: Props) {
  return (
    <label className="nb-table-check" title={title}>
      <input
        {...input}
        type="checkbox"
        ref={(element) => {
          if (element) element.indeterminate = indeterminate;
        }}
      />
      {text && <span>{text}</span>}
    </label>
  );
}
