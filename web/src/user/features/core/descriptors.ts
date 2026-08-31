import type { CoreCopyKey } from './copy';

export const CORE_ROUTE_PATHS = Object.freeze({
  home: '/',
  endpoints: '/endpoints',
  endpointDetail: (endpointId: string) => `/endpoints/${encodeURIComponent(endpointId)}`,
  keys: '/keys',
  models: '/models',
  account: '/account',
});

export interface CoreRouteDescriptor {
  id: 'home' | 'endpoints' | 'endpoint-detail' | 'caller-key' | 'models' | 'account';
  path: string;
  titleKey: CoreCopyKey;
  navigation: boolean;
}

export const CORE_ROUTE_DESCRIPTORS: readonly CoreRouteDescriptor[] = Object.freeze([
  { id: 'home', path: CORE_ROUTE_PATHS.home, titleKey: 'home.signedOutTitle', navigation: true },
  {
    id: 'endpoints',
    path: CORE_ROUTE_PATHS.endpoints,
    titleKey: 'endpoints.title',
    navigation: true,
  },
  {
    id: 'endpoint-detail',
    path: '/endpoints/:endpointId',
    titleKey: 'endpoints.detailsTitle',
    navigation: false,
  },
  { id: 'caller-key', path: CORE_ROUTE_PATHS.keys, titleKey: 'keys.title', navigation: false },
  { id: 'models', path: CORE_ROUTE_PATHS.models, titleKey: 'models.title', navigation: true },
  { id: 'account', path: CORE_ROUTE_PATHS.account, titleKey: 'account.title', navigation: false },
]);
