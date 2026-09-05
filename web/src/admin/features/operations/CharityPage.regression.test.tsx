import { screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { renderWithProviders } from '../../../../test/unit/support';
import { CharityPage } from '../../pages/CharityPage';

vi.mock('./AdminCharityGroups', () => ({
  AdminCharityGroupsPanel: () => <div data-testid="grouped-charity-panel" />,
}));

describe('administrator charity page composition', () => {
  it('opens provenance grouping in its own section with one active filter surface', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => new Response(
      JSON.stringify({ data: [], next_cursor: null }),
      { headers: { 'Content-Type': 'application/json' } },
    )));
    const rendered = await renderWithProviders(<CharityPage />, {
      station: 'admin',
      role: 'admin',
    });

    expect(screen.queryByTestId('grouped-charity-panel')).not.toBeInTheDocument();
    expect(await screen.findByRole('combobox')).toBeInTheDocument();
    await rendered.user.click(screen.getByRole('tab', { name: 'Browse by source' }));
    expect(screen.getByTestId('grouped-charity-panel')).toBeInTheDocument();
    expect(screen.queryByRole('combobox')).not.toBeInTheDocument();
  });
});
