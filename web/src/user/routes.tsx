import { createBrowserRouter } from 'react-router';
import { NotFoundPage } from '@shared/components/NotFoundPage';
import { LoadingState, NoticePage } from '@shared/components/States';
import { RouteErrorPage } from '@shared/components/RouteErrorPage';
import { relativeRoutePath } from '@shared/routing/routeRegistry';
import { UserLayout } from './layouts/UserLayout';

const pathFor = (id: string) => relativeRoutePath('user', id);

export const router = createBrowserRouter([
  {
    path: '/',
    element: <UserLayout />,
    hydrateFallbackElement: <LoadingState />,
    errorElement: <RouteErrorPage station="user" />,
    children: [
      {
        index: true,
        lazy: async () => ({ Component: (await import('./pages/HomePage')).HomePage }),
      },
      {
        path: pathFor('endpoints'),
        lazy: async () => ({ Component: (await import('./pages/EndpointsPage')).EndpointsPage }),
      },
      {
        path: pathFor('endpoint-detail'),
        lazy: async () => ({ Component: (await import('./pages/EndpointsPage')).EndpointsPage }),
      },
      {
        path: pathFor('models'),
        lazy: async () => ({ Component: (await import('./pages/ModelsPage')).ModelsPage }),
      },
      {
        path: pathFor('games'),
        lazy: async () => ({ Component: (await import('./pages/GamesPage')).GamesPage }),
      },
      {
        path: pathFor('charity'),
        lazy: async () => ({ Component: (await import('./pages/CharityPage')).CharityPage }),
      },
      {
        path: pathFor('donation-detail'),
        lazy: async () => ({ Component: (await import('./pages/CharityPage')).CharityPage }),
      },
      {
        path: pathFor('activities'),
        lazy: async () => ({ Component: (await import('./pages/ActivitiesPage')).ActivitiesPage }),
      },
      {
        path: pathFor('caller-key'),
        lazy: async () => ({ Component: (await import('./pages/KeysPage')).KeysPage }),
      },
      {
        path: pathFor('debug'),
        lazy: async () => ({ Component: (await import('./pages/DebugPage')).DebugPage }),
      },
      {
        path: pathFor('logs'),
        lazy: async () => ({ Component: (await import('./pages/LogsPage')).LogsPage }),
      },
      {
        path: pathFor('issues'),
        lazy: async () => ({ Component: (await import('./pages/IssuesPage')).IssuesPage }),
      },
      {
        path: pathFor('account'),
        lazy: async () => ({ Component: (await import('./pages/AccountPage')).AccountPage }),
      },
      {
        path: pathFor('steward'),
        lazy: async () => ({ Component: (await import('./pages/StewardPage')).StewardPage }),
      },
      {
        path: pathFor('privacy'),
        lazy: async () => ({ Component: (await import('./pages/PrivacyPage')).PrivacyPage }),
      },
      {
        path: pathFor('terms'),
        lazy: async () => ({ Component: (await import('./pages/TermsPage')).TermsPage }),
      },
      {
        path: pathFor('maintenance'),
        element: <NoticePage titleKey="common.maintenanceTitle" bodyKey="common.maintenanceBody" />,
      },
      {
        path: pathFor('registration-closed'),
        element: (
          <NoticePage
            titleKey="common.registrationClosedTitle"
            bodyKey="common.registrationClosedBody"
            icon="info"
          />
        ),
      },
      { path: '*', element: <NotFoundPage station="user" /> },
    ],
  },
]);
