import { createBrowserRouter } from 'react-router';
import { NotFoundPage } from '@shared/components/NotFoundPage';
import { LoadingState, NoticePage } from '@shared/components/States';
import { RouteErrorPage } from '@shared/components/RouteErrorPage';
import { UserLayout } from './layouts/UserLayout';

export const router = createBrowserRouter([
  {
    path: '/',
    element: <UserLayout />,
    hydrateFallbackElement: <LoadingState />,
    errorElement: <RouteErrorPage />,
    children: [
      { index: true, lazy: async () => ({ Component: (await import('./pages/HomePage')).HomePage }) },
      { path: 'endpoints', lazy: async () => ({ Component: (await import('./pages/EndpointsPage')).EndpointsPage }) },
      { path: 'models', lazy: async () => ({ Component: (await import('./pages/ModelsPage')).ModelsPage }) },
      { path: 'games', lazy: async () => ({ Component: (await import('./pages/GamesPage')).GamesPage }) },
      { path: 'charity', lazy: async () => ({ Component: (await import('./pages/CharityPage')).CharityPage }) },
      { path: 'keys', lazy: async () => ({ Component: (await import('./pages/KeysPage')).KeysPage }) },
      { path: 'debug', lazy: async () => ({ Component: (await import('./pages/DebugPage')).DebugPage }) },
      { path: 'logs', lazy: async () => ({ Component: (await import('./pages/LogsPage')).LogsPage }) },
      { path: 'issues', lazy: async () => ({ Component: (await import('./pages/IssuesPage')).IssuesPage }) },
      { path: 'account', lazy: async () => ({ Component: (await import('./pages/AccountPage')).AccountPage }) },
      { path: 'steward', lazy: async () => ({ Component: (await import('./pages/StewardPage')).StewardPage }) },
      { path: 'privacy', lazy: async () => ({ Component: (await import('./pages/PrivacyPage')).PrivacyPage }) },
      { path: 'terms', lazy: async () => ({ Component: (await import('./pages/TermsPage')).TermsPage }) },
      { path: 'maintenance', element: <NoticePage titleKey="common.maintenanceTitle" bodyKey="common.maintenanceBody" /> },
      { path: 'registration-closed', element: <NoticePage titleKey="common.registrationClosedTitle" bodyKey="common.registrationClosedBody" /> },
      { path: '*', element: <NotFoundPage /> },
    ],
  },
]);
