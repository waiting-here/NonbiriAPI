import { type AriaAttributes, type ReactNode } from 'react';

export function Panel({
  children,
  tone,
  className = '',
  'aria-label': ariaLabel,
  'aria-busy': busy,
}: {
  children: ReactNode;
  tone?: 'danger';
  className?: string;
  'aria-label'?: string;
  'aria-busy'?: AriaAttributes['aria-busy'];
}) {
  return (
    <section
      className={`nb-panel${tone ? ` nb-panel--${tone}` : ''}${className ? ` ${className}` : ''}`}
      aria-label={ariaLabel}
      aria-busy={busy}
    >
      {children}
    </section>
  );
}

export function PanelHead({
  title,
  description,
  actions,
  level = 2,
}: {
  title: ReactNode;
  description?: ReactNode;
  actions?: ReactNode;
  level?: 2 | 3;
}) {
  const H = level === 2 ? 'h2' : 'h3';
  return (
    <div className="nb-panel__head">
      <div>
        <H>{title}</H>
        {description ? <p>{description}</p> : null}
      </div>
      {actions ? <div className="nb-inline">{actions}</div> : null}
    </div>
  );
}

export const PanelBody = ({ children, flush }: { children: ReactNode; flush?: boolean }) => (
  <div className={`nb-panel__body${flush ? ' nb-panel__body--flush' : ''}`}>{children}</div>
);
export const PanelFoot = ({ children }: { children: ReactNode }) => (
  <div className="nb-panel__foot">{children}</div>
);
