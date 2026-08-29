import { screen } from '@testing-library/react';
import { describe, expect, test } from 'vitest';
import { NotFoundPage } from '../../src/shared/components/NotFoundPage';
import { installJsonFetchFixtures, renderWithProviders } from './support';

describe('nested station 404 leaf', () => {
  test.each([
    ['user', '/'] as const,
    ['admin', '/'] as const,
  ])('%s keeps branding and config in the parent shell', async (station, expectedHref) => {
    const fetchMock = installJsonFetchFixtures([]);
    await renderWithProviders(<NotFoundPage station={station} />, { station });

    expect(screen.getByRole('heading', { name: 'Page not found' })).toBeInTheDocument();
    expect(screen.getByRole('link', { name: 'Back to home' })).toHaveAttribute('href', expectedHref);
    expect(document.querySelector('.nb-brand')).not.toBeInTheDocument();
    expect(fetchMock).not.toHaveBeenCalled();
  });
});
