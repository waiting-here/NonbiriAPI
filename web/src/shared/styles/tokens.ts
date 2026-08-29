/**
 * Typed names for the CSS token contract.
 *
 * Components consume CSS variables at runtime so light/dark and local density
 * preferences can change without rebuilding. This map is intentionally a
 * name registry, not a second palette that can drift from tokens.css.
 */
export const designTokens = {
  color: {
    background: '--nb-color-bg',
    surface: '--nb-color-surface',
    surfaceRaised: '--nb-color-surface-raised',
    border: '--nb-color-border',
    text: '--nb-color-text',
    textMuted: '--nb-color-text-muted',
    textSubtle: '--nb-color-text-subtle',
    onAccent: '--nb-color-on-accent',
    accentContrast: '--nb-color-accent-contrast',
    accent: '--nb-color-accent',
    accentHover: '--nb-color-accent-hover',
    accentSoft: '--nb-color-accent-soft',
    surfaceAlt: '--nb-color-surface-alt',
    brandStart: '--nb-color-brand-start',
    brandEnd: '--nb-color-brand-end',
    success: '--nb-color-success',
    warning: '--nb-color-warning',
    danger: '--nb-color-danger',
    onDanger: '--nb-color-on-danger',
    info: '--nb-color-info',
  },
  spacing: {
    zero: '--nb-space-0',
    xs: '--nb-space-1',
    sm: '--nb-space-2',
    md: '--nb-space-4',
    lg: '--nb-space-6',
    xl: '--nb-space-8',
  },
  radius: {
    none: '--nb-radius-none',
    sm: '--nb-radius-sm',
    md: '--nb-radius-md',
    lg: '--nb-radius-lg',
    pill: '--nb-radius-pill',
  },
  elevation: {
    none: '--nb-elevation-0',
    card: '--nb-elevation-1',
    overlay: '--nb-elevation-overlay',
  },
  typography: {
    fontSans: '--nb-font-sans',
    fontMono: '--nb-font-mono',
    sizeXs: '--nb-font-size-xs',
    sizeSm: '--nb-font-size-sm',
    sizeMd: '--nb-font-size-md',
    sizeLg: '--nb-font-size-lg',
    sizeXl: '--nb-font-size-xl',
    size2xl: '--nb-font-size-2xl',
    sizeHero: '--nb-font-size-hero',
    lineTight: '--nb-line-tight',
    lineNormal: '--nb-line-normal',
    lineRelaxed: '--nb-line-relaxed',
    weightRegular: '--nb-font-weight-regular',
    weightMedium: '--nb-font-weight-medium',
    weightSemibold: '--nb-font-weight-semibold',
    weightBold: '--nb-font-weight-bold',
  },
  focus: {
    ring: '--nb-focus-ring',
  },
  layout: {
    readable: '--nb-content-readable',
    wide: '--nb-content-wide',
    shellBreakpoint: '--nb-shell-breakpoint',
  },
  layering: {
    base: '--nb-z-base',
    sticky: '--nb-z-sticky',
    drawer: '--nb-z-drawer',
    overlay: '--nb-z-overlay',
    dialog: '--nb-z-dialog',
    toast: '--nb-z-toast',
  },
  preference: {
    densityFactor: '--nb-density-factor',
    fontScale: '--nb-font-scale',
  },
  motion: {
    micro: '--nb-motion-micro',
    standard: '--nb-motion-standard',
    scene: '--nb-motion-scene',
  },
} as const;

export type DesignTokens = typeof designTokens;
export type DesignToken = {
  [Group in keyof DesignTokens]: DesignTokens[Group][keyof DesignTokens[Group]];
}[keyof DesignTokens];

export const cssVar = (token: DesignToken): `var(${string})` => `var(${token})`;

/**
 * Drawer breakpoints are shared by shell CSS and focus/media-query behavior.
 * Keep the values in one typed place so expanding a viewport always closes a
 * drawer before its trigger becomes hidden.
 */
export const SHELL_DRAWER_MEDIA_QUERY = {
  user: '(max-width: 70rem)',
  admin: '(max-width: 64rem)',
} as const;
