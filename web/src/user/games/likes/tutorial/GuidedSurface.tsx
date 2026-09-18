import { useLayoutEffect, useRef, type ReactNode, type SyntheticEvent } from 'react';

export function GuidedSurface({
  target,
  value,
  step,
  onAction,
  children,
}: {
  readonly target: string;
  readonly value?: string;
  readonly step: number;
  readonly onAction: (step: number, target: string) => void;
  readonly children: ReactNode;
}) {
  const root = useRef<HTMLDivElement>(null);
  useLayoutEffect(() => {
    const controls = [
      ...(root.current?.querySelectorAll<HTMLElement>('button, input, select, a, summary') ?? []),
    ];
    const previous = controls.map((node) => ({ node, inert: node.inert }));
    for (const node of controls) {
      const active = node.dataset.guide === target;
      node.inert = !active;
      node.toggleAttribute('data-guide-active', active);
      if (active) node.setAttribute('aria-describedby', 'likes-tutorial-tip');
    }
    const selected = controls.find((node) => node.dataset.guide === target);
    selected?.scrollIntoView?.({ block: 'center', behavior: 'instant' });
    selected?.focus({ preventScroll: true });
    return () => {
      for (const { node, inert } of previous) {
        node.inert = inert;
        node.removeAttribute('data-guide-active');
        node.removeAttribute('aria-describedby');
      }
    };
  }, [target, step]);
  const control = (event: SyntheticEvent) =>
    event.target instanceof Element
      ? event.target.closest<HTMLElement>('button, input, select, a, summary')
      : null;
  const guard = (event: SyntheticEvent) => {
    const node = control(event);
    if (
      node &&
      (node.dataset.guide !== target ||
        (event.type === 'change' && value !== undefined && 'value' in node && node.value !== value))
    ) {
      event.preventDefault();
      event.stopPropagation();
    }
  };
  return (
    <div
      ref={root}
      className="likes-tutorial-surface"
      data-tutorial-target={target}
      onClickCapture={guard}
      onChangeCapture={guard}
      onClick={(event) => {
        const node = control(event);
        if (node?.tagName === 'BUTTON' && node.dataset.guide === target) onAction(step, target);
      }}
      onChange={(event) => {
        const node = control(event);
        if (node?.dataset.guide === target) onAction(step, target);
      }}
    >
      {children}
    </div>
  );
}
