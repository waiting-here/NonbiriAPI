import type { ReactNode } from 'react';
import { Icon } from '../Icon';
import './stepper.css';

export type StepState = 'done' | 'current' | 'todo';

export interface StepperStep {
  id: string;
  title: ReactNode;
  description?: ReactNode;
  state: StepState;
  /** Shown only for the current step, so one obvious next action stands out. */
  action?: ReactNode;
  /** Shown for finished steps, for example a quiet "View" link. */
  doneAction?: ReactNode;
  /** Extra content that stays visible for this step, such as an address to copy. */
  children?: ReactNode;
}

/** Ordered task steps. Horizontal on wide containers, vertical on narrow ones. */
export function Stepper({
  steps,
  label,
  doneLabel,
  className = '',
}: {
  steps: StepperStep[];
  label: string;
  /** Accessible text for the check mark on finished steps. */
  doneLabel: string;
  className?: string;
}) {
  return (
    <ol className={`nb-stepper${className ? ` ${className}` : ''}`} aria-label={label}>
      {steps.map((step, index) => (
        <li
          key={step.id}
          className="nb-stepper__step"
          data-state={step.state}
          aria-current={step.state === 'current' ? 'step' : undefined}
        >
          <span className="nb-stepper__marker">
            {step.state === 'done' ? (
              <Icon name="check" label={doneLabel} />
            ) : (
              <span aria-hidden="true">{index + 1}</span>
            )}
          </span>
          <div className="nb-stepper__body">
            <strong className="nb-stepper__title">{step.title}</strong>
            {step.description ? (
              <p className="nb-stepper__description">{step.description}</p>
            ) : null}
            {step.children}
            {step.state === 'current' && step.action ? (
              <div className="nb-stepper__action">{step.action}</div>
            ) : null}
            {step.state === 'done' && step.doneAction ? (
              <div className="nb-stepper__action">{step.doneAction}</div>
            ) : null}
            {step.state === 'todo' && step.action ? (
              <div className="nb-stepper__action nb-stepper__action--later">{step.action}</div>
            ) : null}
          </div>
        </li>
      ))}
    </ol>
  );
}
