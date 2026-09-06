import { screen, waitFor } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { renderWithProviders } from '../../../test/unit/support';
import { AdminLayout } from './AdminLayout';

describe('anonymous administrator branding', () => {
  it.each([true, false])(
    'keeps login available with public branding success=%s',
    async (available) => {
      const icon = document.createElement('link');
      icon.rel = 'icon';
      document.head.append(icon);
      const requests: string[] = [];
      vi.stubGlobal(
        'fetch',
        vi.fn(async (input: RequestInfo | URL) => {
          const url = String(input);
          requests.push(url);
          const branding = url.endsWith('/admin/api/branding');
          return new Response(
            JSON.stringify(
              branding && available
                ? {
                    site_name: 'Custom public name',
                    site_logo_url: 'https://cdn.example.test/icon.svg',
                  }
                : { error: { code: 'unauthorized', message: 'Sign in required' } },
            ),
            {
              status: branding && available ? 200 : 401,
              headers: { 'Content-Type': 'application/json' },
            },
          );
        }),
      );
      try {
        await renderWithProviders(<AdminLayout />, { station: 'admin', role: 'anonymous' });
        expect(await screen.findByRole('button', { name: 'Sign in' })).toBeInTheDocument();
        if (available) {
          await waitFor(() =>
            expect(icon).toHaveAttribute('href', 'https://cdn.example.test/icon.svg'),
          );
          expect(screen.getAllByText('Custom public name').length).toBeGreaterThan(0);
        }
        expect(requests).toContain('/admin/api/branding');
        expect(requests).not.toContain('/admin/api/config');
      } finally {
        icon.remove();
      }
    },
  );
});
