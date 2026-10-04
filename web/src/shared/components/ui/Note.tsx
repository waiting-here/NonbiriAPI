import { type ReactNode } from 'react';

export function Note({
  tone = 'info',
  title,
  children,
  action,
}: {
  tone?: 'info' | 'warn' | 'bad' | 'ok';
  title?: ReactNode;
  children?: ReactNode;
  action?: ReactNode;
}) {
  const icon = { info: 'i', warn: '!', bad: '!', ok: '✓' }[tone];
  return (
    <div
      className={`nb-note${tone === 'info' ? '' : ` nb-note--${tone}`}`}
      role={tone === 'bad' || tone === 'warn' ? 'alert' : 'status'}
    >
      <span className="nb-note__icon" aria-hidden="true">
        {icon}
      </span>
      <div>
        {title ? <strong>{title}</strong> : null}
        {children}
      </div>
      {action ?? <span />}
    </div>
  );
}
