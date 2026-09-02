import { createBrowserRouter } from 'react-router';
import { NotFoundPage } from '@shared/components/NotFoundPage';
import { LoadingState } from '@shared/components/States';
import { RouteErrorPage } from '@shared/components/RouteErrorPage';
import { relativeRoutePath } from '@shared/routing/routeRegistry';
import { AdminLayout } from './layouts/AdminLayout';

const pathFor = (id: string) => relativeRoutePath('admin', id);

export const router = createBrowserRouter([
  {
    path: '/',
    element: <AdminLayout />,
    hydrateFallbackElement: <LoadingState />,
    errorElement: <RouteErrorPage station="admin" />,
    children: [
      { index: true, lazy: async () => ({ Component: (await import('./pages/DashboardPage')).DashboardPage }) },
      { path: pathFor('admin-users'), lazy: async () => ({ Component: (await import('./pages/UsersPage')).UsersPage }) },
      { path: pathFor('admin-logs'), lazy: async () => ({ Component: (await import('./pages/LogsPage')).LogsPage }) },
      { path: pathFor('admin-endpoints'), lazy: async () => ({ Component: (await import('./pages/EndpointsPage')).EndpointsPage }) },
      { path: pathFor('admin-alerts'), lazy: async () => ({ Component: (await import('./pages/AlertsPage')).AlertsPage }) },
      { path: pathFor('admin-settings'), lazy: async () => ({ Component: (await import('./pages/SettingsPage')).SettingsPage }) },
      { path: pathFor('admin-games'), lazy: async () => ({ Component: (await import('./pages/GamesPage')).GamesPage }) },
      { path: pathFor('admin-charity'), lazy: async () => ({ Component: (await import('./pages/CharityPage')).CharityPage }) },
      { path: pathFor('admin-activities'), lazy: async () => ({ Component: (await import('./pages/ActivitiesPage')).ActivitiesPage }) },
      { path: pathFor('admin-reports'), lazy: async () => ({ Component: (await import('./pages/ReportsPage')).ReportsPage }) },
      { path: pathFor('admin-report-detail'), lazy: async () => ({ Component: (await import('./pages/ReportDetailPage')).ReportDetailPage }) },
      { path: pathFor('admin-announcements'), lazy: async () => ({ Component: (await import('./pages/AnnouncementsPage')).AnnouncementsPage }) },
      { path: pathFor('admin-announcement-detail'), lazy: async () => ({ Component: (await import('./pages/AnnouncementDetailPage')).AnnouncementDetailPage }) },
      { path: '*', element: <NotFoundPage station="admin" /> },
    ],
  },
]);
