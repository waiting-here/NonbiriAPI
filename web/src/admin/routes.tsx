import { createBrowserRouter } from 'react-router';
import { NotFoundPage } from '@shared/components/NotFoundPage';
import { LoadingState } from '@shared/components/States';
import { RouteErrorPage } from '@shared/components/RouteErrorPage';
import { AdminLayout } from './layouts/AdminLayout';

export const router = createBrowserRouter([
  {
    path: '/',
    element: <AdminLayout />,
    hydrateFallbackElement: <LoadingState />,
    errorElement: <RouteErrorPage />,
    children: [
      { index: true, lazy: async () => ({ Component: (await import('./pages/DashboardPage')).DashboardPage }) },
      { path: 'users', lazy: async () => ({ Component: (await import('./pages/UsersPage')).UsersPage }) },
      { path: 'logs', lazy: async () => ({ Component: (await import('./pages/LogsPage')).LogsPage }) },
      { path: 'endpoints', lazy: async () => ({ Component: (await import('./pages/EndpointsPage')).EndpointsPage }) },
      { path: 'alerts', lazy: async () => ({ Component: (await import('./pages/AlertsPage')).AlertsPage }) },
      { path: 'settings', lazy: async () => ({ Component: (await import('./pages/SettingsPage')).SettingsPage }) },
      { path: 'charity', lazy: async () => ({ Component: (await import('./pages/CharityPage')).CharityPage }) },
      { path: '*', element: <NotFoundPage /> },
    ],
  },
]);
