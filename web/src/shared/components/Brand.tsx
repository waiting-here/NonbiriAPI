import { useEffect, useState } from 'react';
import type { ReactNode } from 'react';
import markURL from '@shared/assets/nonbiri-mark.svg';
import { isPublicLogoURL } from '@shared/query/publicConfig';

// Backwards-compatible component-level name; validation remains one shared
// policy boundary for config normalization, image rendering, and favicon use.
export const isPublicHTTPSURL = isPublicLogoURL;

interface BrandProps {
  siteName: string;
  siteLogoURL?: string;
  href?: string;
  suffix?: ReactNode;
  compact?: boolean;
  className?: string;
}

/**
 * Renders operator branding without allowing a broken or non-public URL to
 * remove the local identity. Configured remote logos use anonymous requests
 * and fall back to the bundled mark after a load failure.
 */
export function Brand({ siteName, siteLogoURL, href, suffix, compact = false, className = '' }: BrandProps) {
  const [failedLogoURL, setFailedLogoURL] = useState('');
  const remoteLogo = isPublicLogoURL(siteLogoURL) && failedLogoURL !== siteLogoURL ? siteLogoURL : '';
  const content = (
    <>
      <img
        className="nb-brand__mark"
        alt=""
        aria-hidden="true"
        crossOrigin={remoteLogo ? 'anonymous' : undefined}
        referrerPolicy={remoteLogo ? 'no-referrer' : undefined}
        src={remoteLogo || markURL}
        onError={() => {
          if (remoteLogo) setFailedLogoURL(remoteLogo);
        }}
      />
      {!compact ? <span className="nb-brand__name">{siteName}</span> : null}
      {suffix}
    </>
  );
  const classes = `nb-brand${className ? ` ${className}` : ''}`;
  return href ? (
    <a className={classes} href={href} aria-label={siteName}>
      {content}
    </a>
  ) : (
    <span className={classes} aria-label={siteName}>
      {content}
    </span>
  );
}

/** Keep the browser tab icon in sync with the same safe logo policy. */
export function useBrandFavicon(siteLogoURL?: string): void {
  useEffect(() => {
    const link = document.querySelector<HTMLLinkElement>('link[rel="icon"]');
    if (!link) return;
    const fallback = markURL;
    const remoteLogo = isPublicLogoURL(siteLogoURL) ? siteLogoURL : '';
    // Set request policy before assigning href so a remote favicon's first
    // fetch cannot race ahead with browser defaults.
    if (remoteLogo) {
      link.setAttribute('crossorigin', 'anonymous');
      link.setAttribute('referrerpolicy', 'no-referrer');
    } else {
      link.removeAttribute('crossorigin');
      link.removeAttribute('referrerpolicy');
    }
    if (!remoteLogo) {
      link.href = fallback;
      return;
    }
    const onError = () => {
      link.removeAttribute('crossorigin');
      link.removeAttribute('referrerpolicy');
      link.href = fallback;
      link.removeEventListener('error', onError);
    };
    // Register before assigning href: cached failures can dispatch in the
    // same turn as the assignment.
    link.addEventListener('error', onError);
    link.href = remoteLogo;
    return () => link.removeEventListener('error', onError);
  }, [siteLogoURL]);
}
