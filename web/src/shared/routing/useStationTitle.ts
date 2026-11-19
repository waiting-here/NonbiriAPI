import { useEffect } from 'react';
import { useTranslation } from 'react-i18next';
import { matchPath, useLocation } from 'react-router';
import { ROUTE_REGISTRY, type RouteDescriptor, type Station } from './routeRegistry';

// An open tab stays identifiable even while its page bundle or data is loading.
export function useStationTitle(station: Station, siteName: string, signedIn: boolean) {
  const { pathname } = useLocation();
  const { t } = useTranslation();
  useEffect(() => {
    const routes: readonly RouteDescriptor[] = ROUTE_REGISTRY[station];
    const match = routes
      .filter((route) => route.labelKey && (signedIn || route.access === 'public'))
      .filter((route) => matchPath({ path: route.path, end: route.path === '/' }, pathname))
      .sort((a, b) => b.path.length - a.path.length)[0];
    document.title = [match?.labelKey && t(match.labelKey), siteName, t(`${station}.shell.title`)]
      .filter(Boolean)
      .join(' · ');
  }, [pathname, signedIn, siteName, station, t]);
}
