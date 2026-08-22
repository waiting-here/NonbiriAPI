import { createBrowserRouter } from 'react-router';
import { NotFoundPage } from '@shared/components/NotFoundPage';
import { NoticePage } from '@shared/components/States';
import { RouteErrorPage } from '@shared/components/RouteErrorPage';
import { UserLayout } from './layouts/UserLayout';
import { HomePage } from './pages/HomePage';
import { EndpointsPage } from './pages/EndpointsPage';
import { ModelsPage } from './pages/ModelsPage';
import { KeysPage } from './pages/KeysPage';
import { LogsPage } from './pages/LogsPage';
import { IssuesPage } from './pages/IssuesPage';
import { AccountPage } from './pages/AccountPage';
import { PrivacyPage } from './pages/PrivacyPage';
import { TermsPage } from './pages/TermsPage';
import { StewardPage } from './pages/StewardPage';
import { CharityPage } from './pages/CharityPage';

export const router = createBrowserRouter([
  {
    path: '/',
    element: <UserLayout />,
    errorElement: <RouteErrorPage />,
    children: [
      { index: true, element: <HomePage /> },
      { path: 'endpoints', element: <EndpointsPage /> },
      { path: 'models', element: <ModelsPage /> },
      { path: 'charity', element: <CharityPage /> },
      { path: 'keys', element: <KeysPage /> },
      { path: 'logs', element: <LogsPage /> },
      { path: 'issues', element: <IssuesPage /> },
      { path: 'account', element: <AccountPage /> },
      { path: 'steward', element: <StewardPage /> },
      { path: 'privacy', element: <PrivacyPage /> },
      { path: 'terms', element: <TermsPage /> },
      { path: 'maintenance', element: <NoticePage titleKey="common.maintenanceTitle" bodyKey="common.maintenanceBody" /> },
      { path: 'registration-closed', element: <NoticePage titleKey="common.registrationClosedTitle" bodyKey="common.registrationClosedBody" /> },
      { path: '*', element: <NotFoundPage /> },
    ],
  },
]);
