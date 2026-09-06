import type { ReactNode, SVGProps } from 'react';

export const ICON_NAMES = [
  'home',
  'resources',
  'models',
  'charity',
  'activities',
  'games',
  'diagnostics',
  'account',
  'endpoints',
  'keys',
  'logs',
  'issues',
  'steward',
  'dashboard',
  'users',
  'alerts',
  'settings',
  'reports',
  'announcements',
  'menu',
  'close',
  'chevron-down',
  'arrow-left',
  'check',
  'info',
  'warning',
  'error',
  'empty',
  'maintenance',
  'spark',
  'logout',
] as const;

export type IconName = (typeof ICON_NAMES)[number];

type IconProps = Omit<SVGProps<SVGSVGElement>, 'name'> & {
  name: IconName;
  label?: string;
};

const PATHS: Record<IconName, ReactNode> = {
  home: <path d="m3 10 9-7 9 7v10a1 1 0 0 1-1 1h-5v-6H9v6H4a1 1 0 0 1-1-1z" />,
  resources: <path d="M4 5h16v4H4zm0 6h16v8H4zm4 3h8" />,
  models: <path d="M12 3 4 7l8 4 8-4-8-4Zm-8 8 8 4 8-4M4 15l8 4 8-4" />,
  charity: <path d="M20.8 8.6c0 5.4-8.8 10.4-8.8 10.4S3.2 14 3.2 8.6A4.6 4.6 0 0 1 12 6.5a4.6 4.6 0 0 1 8.8 2.1ZM12 8v6m-3-3h6" />,
  activities: <path d="M4 19h16M6 16V9m6 7V5m6 11v-4" />,
  games: <path d="m7 9 2-3h6l2 3 3 2-2 7h-4l-2-2-2 2H6l-2-7zm1 2v4m-2-2h4m8-2h.01m2 2h.01" />,
  diagnostics: <path d="M4 12h3l2-6 4 12 2-6h5" />,
  account: <path d="M12 12a4 4 0 1 0 0-8 4 4 0 0 0 0 8Zm-7 9a7 7 0 0 1 14 0" />,
  endpoints: <path d="M8 7V4h8v3M5 7h14v13H5zm3 4h8m-8 4h5" />,
  keys: <><circle cx="8" cy="8" r="5" /><path d="m11.5 11.5 8 8H22V17h-3v-3h-3m-9-7h.01" /></>,
  logs: <path d="M5 4h14v16H5zm3 4h8m-8 4h8m-8 4h5" />,
  issues: <path d="M12 4 2.5 20h19L12 4Zm0 5v5m0 3h.01" />,
  steward: <path d="M12 3 5 6v5c0 4.4 2.8 8.1 7 10 4.2-1.9 7-5.6 7-10V6zm-3 9 2 2 4-4" />,
  dashboard: <path d="M4 4h6v7H4zm10 0h6v4h-6zM4 15h6v5H4zm10-4h6v9h-6z" />,
  users: <path d="M16 20v-1a4 4 0 0 0-4-4H7a4 4 0 0 0-4 4v1m6-9a4 4 0 1 0 0-8 4 4 0 0 0 0 8Zm6-7a3 3 0 0 1 0 6m2 10v-1a4 4 0 0 0-3-3.9" />,
  alerts: <path d="M18 8a6 6 0 0 0-12 0c0 7-3 7-3 9h18c0-2-3-2-3-9Zm-8 13h4" />,
  settings: <path d="m12 3 1.2 2.3 2.5.6.5 2.5L18 10l-1.8 2 1.8 2-1.8 1.6-.5 2.5-2.5.6L12 21l-1.2-2.3-2.5-.6-.5-2.5L6 14l1.8-2L6 10l1.8-1.6.5-2.5 2.5-.6zM12 15a3 3 0 1 0 0-6 3 3 0 0 0 0 6Z" />,
  reports: <path d="M5 4h14v16H5zm3 4h8m-8 4h8m-8 4h5" />,
  announcements: <path d="M4 11v2h3l8 4V7l-8 4zm11-1 4-2v6l-4-2M7 13l1 5h3l-1-5" />,
  menu: <path d="M4 7h16M4 12h16M4 17h16" />,
  close: <path d="m5 5 14 14M19 5 5 19" />,
  'chevron-down': <path d="m6 9 6 6 6-6" />,
  'arrow-left': <path d="M19 12H5m6-6-6 6 6 6" />,
  check: <path d="m5 12 4 4L19 6" />,
  info: <path d="M12 17v-5m0-4h.01M21 12a9 9 0 1 1-18 0 9 9 0 0 1 18 0Z" />,
  warning: <path d="M12 4 2.5 20h19L12 4Zm0 6v4m0 3h.01" />,
  error: <path d="M12 4 2.5 20h19L12 4Zm-3 6 6 6m0-6-6 6" />,
  empty: <path d="M4 6h16v12H4zm4 4h8m-8 4h5" />,
  maintenance: <path d="m4 20 10-10 4 4L8 24H4zm10-10 3-3 4 4-3 3M6 4h6m-8 4h5" />,
  spark: <path d="m12 3 1.4 6.6L20 12l-6.6 1.4L12 20l-1.4-6.6L4 12l6.6-2.4z" />,
  logout: <path d="M10 4H5v16h5m5-4 4-4-4-4m4 4H9" />,
};

export function Icon({ name, label, className, ...props }: IconProps) {
  const labelled = Boolean(label);
  return (
    <svg
      {...props}
      className={`nb-icon${className ? ` ${className}` : ''}`}
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeLinecap="round"
      strokeLinejoin="round"
      strokeWidth="1.8"
      role={labelled ? 'img' : undefined}
      aria-hidden={labelled ? undefined : true}
      aria-label={label}
      focusable="false"
    >
      {label ? <title>{label}</title> : null}
      {PATHS[name]}
    </svg>
  );
}
