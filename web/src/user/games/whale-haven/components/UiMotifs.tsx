import { useId, type ReactNode } from 'react';
import { X } from './Icons';

export function WhaleMark({ className }: { className?: string }) {
  return (
    <svg className={className} viewBox="0 0 56 36" aria-hidden="true">
      <path
        fill="currentColor"
        d="M4 17C7 6 20 2 32 7c5 2 8 6 13 7 2-2 2-6 0-9 4 0 7 2 7 5l4-5c2 6-1 10-6 12-2 10-12 16-24 15C12 31 2 27 4 17Z"
      />
      <path
        d="M12 22c3 3 9 5 13 5"
        fill="none"
        stroke="white"
        strokeWidth="1.1"
        strokeLinecap="round"
        opacity=".75"
      />
      <circle cx="14" cy="17.8" r="1.1" fill="white" />
    </svg>
  );
}

export function StarWave({ className = '' }: { className?: string }) {
  return (
    <div className={`star-wave ${className}`} aria-hidden="true">
      <svg viewBox="0 0 24 24">
        <path
          d="M12 1c1.4 6.7 4.3 9.6 11 11-6.7 1.4-9.6 4.3-11 11C10.6 16.3 7.7 13.4 1 12 7.7 10.6 10.6 7.7 12 1Z"
          fill="currentColor"
        />
      </svg>
    </div>
  );
}

export function NoticeHeading({
  title,
  id,
  onClose,
}: {
  title: string;
  id: string;
  onClose?: () => void;
}) {
  const paint = useId();
  return (
    <header className="notice-heading">
      <svg className="notice-ribbon" viewBox="0 0 112 78" aria-hidden="true">
        <defs>
          <linearGradient id={paint} x2=".8" y2="1">
            <stop stopColor="#91c6ff" />
            <stop offset="1" stopColor="#3b91e8" />
          </linearGradient>
        </defs>
        <path d="M48 33 22 75l16-7 7 8 12-37L75 72l5-12 12 1-33-29Z" fill={`url(#${paint})`} />
        <path
          d="M53 24C39 7 10 0 5 11 2 20 4 37 9 42c7 6 33-4 45-11m2-6C68 10 99 1 105 11c4 8 2 22-2 27-5 6-32 1-47-7"
          fill={`url(#${paint})`}
        />
        <path
          d="M14 36c10-7 24-8 37-8m51 6c-12-5-24-6-39-5"
          fill="none"
          stroke="#3c81ce"
          strokeWidth="2"
          opacity=".45"
        />
        <path
          d="M49 22q8-5 15 0l1 13q-6 7-16 0Z"
          fill="#7fbaff"
          stroke="#ceeaff"
          strokeWidth="1.3"
        />
      </svg>
      <h2 id={id}>{title}</h2>
      {onClose && (
        <button type="button" className="notice-close" onClick={onClose} aria-label="关闭">
          <X size={20} aria-hidden="true" />
        </button>
      )}
      <StarWave />
    </header>
  );
}

export function UiSwitch({
  checked,
  onChange,
  children,
}: {
  checked: boolean;
  onChange: () => void;
  children: ReactNode;
}) {
  return (
    <label className="ui-switch-row">
      <span>{children}</span>
      <input
        className="ui-switch"
        type="checkbox"
        role="switch"
        checked={checked}
        onChange={onChange}
      />
    </label>
  );
}
