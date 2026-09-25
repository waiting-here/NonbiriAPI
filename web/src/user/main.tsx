import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';
import { RouterProvider } from 'react-router';
import { QueryClientProvider } from '@tanstack/react-query';
import { ThemeProvider } from '@shared/theme/ThemeProvider';
import { ToastProvider } from '@shared/components/Toast';
import { TimeContextProvider } from '@shared/components/TimeContext';
import { createQueryClient } from '@shared/query/client';
import '@shared/styles/index.css';
import '@shared/styles/stations/user-shell.css';
import './i18n';
import { router } from './routes';

const root = document.getElementById('root');
if (!root) {
  throw new Error('Root element not found');
}

createRoot(root).render(
  <StrictMode>
    <ThemeProvider>
      <ToastProvider>
        <QueryClientProvider client={createQueryClient()}>
          <TimeContextProvider station="user">
            <RouterProvider router={router} />
          </TimeContextProvider>
        </QueryClientProvider>
      </ToastProvider>
    </ThemeProvider>
  </StrictMode>,
);
