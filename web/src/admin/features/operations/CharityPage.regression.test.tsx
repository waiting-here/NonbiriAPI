import { screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { renderWithProviders } from '../../../../test/unit/support';
import { CharityPage } from '../../pages/CharityPage';

vi.mock('@shared/components/CharityManagement', () => ({
  CharityManagement: ({ frame }: { frame: string }) => (
    <div data-testid="legacy-charity-management">{frame}</div>
  ),
}));

vi.mock('./AdminCharityGroups', () => ({
  AdminCharityGroupsPanel: () => <div data-testid="grouped-charity-panel" />,
}));

describe('administrator charity page composition', () => {
  it('keeps the existing management surface alongside provenance grouping', async () => {
    await renderWithProviders(<CharityPage />, {
      station: 'admin',
      role: 'admin',
    });

    expect(screen.getByTestId('legacy-charity-management')).toHaveTextContent('admin');
    expect(screen.getByTestId('grouped-charity-panel')).toBeInTheDocument();
  });
});
