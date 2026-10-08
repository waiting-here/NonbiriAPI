import type { ComponentProps } from 'react';

export type ButtonVariant =
  'primary' | 'secondary' | 'danger' | 'danger-outline' | 'ghost' | 'link';

export interface ButtonProps extends ComponentProps<'button'> {
  variant?: ButtonVariant;
  size?: 'sm';
  block?: boolean;
}

export function Button({ variant = 'secondary', size, block, className, ...props }: ButtonProps) {
  return (
    <button
      {...props}
      className={[
        'nb-btn',
        `nb-btn--${variant}`,
        size ? `nb-btn--${size}` : '',
        block ? 'nb-btn--block' : '',
        className,
      ]
        .filter(Boolean)
        .join(' ')}
    />
  );
}
