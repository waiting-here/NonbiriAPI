import { act, screen, waitFor } from '@testing-library/react';
import { useLocation, useNavigate } from 'react-router';
import { describe, expect, it } from 'vitest';
import { renderWithProviders } from '../../../test/unit/support';
import { SectionTabs } from './SectionTabs';
import type { UserNavGroup } from '../navigation';
import { clearManagementSession } from '@shared/charityManagement';

function Pages({ group }: { group: UserNavGroup }) {
  const location = useLocation();
  const navigate = useNavigate();
  return (
    <>
      <SectionTabs key={location.pathname} group={group} />
      <output data-testid="address">{location.pathname + location.search + location.hash}</output>
      <button onClick={() => navigate(-1)}>Browser back</button>
      <button onClick={() => navigate(location.pathname)}>Clear query</button>
      <button onClick={() => navigate('/issues?state=current')}>Direct issue link</button>
    </>
  );
}

const sessionKey = ['user', 'session'];
function identity(id: string, level = 5, revision = '1') {
  return { user: { id, effective_level: level, steward_permissions: { revision } } };
}

describe('sibling list navigation', () => {
  it('restores filters, selected records, and focus, including browser back and direct entry', async () => {
    const { user, queryClient } = await renderWithProviders(<Pages group="records" />, {
      station: 'user',
      role: 'user',
      route: '/issues?state=closed&page=3&page_size=10#history',
    });
    act(() => {
      queryClient.setQueryData(sessionKey, identity('101'));
    });
    const link = (name: string) => screen.getByRole('link', { name });
    const url = screen.getByTestId('address');
    expect(link('Issues')).toHaveAttribute('href', url.textContent);
    await user.click(link('Calls'));
    expect(link('Calls')).toHaveFocus();
    expect(link('Issues')).toHaveAttribute(
      'href',
      '/issues?state=closed&page=3&page_size=10#history',
    );
    await user.click(screen.getByRole('button', { name: 'Browser back' }));
    expect(url).toHaveTextContent('/issues?state=closed&page=3&page_size=10#history');
    expect(link('Calls')).toHaveFocus();
    await user.click(link('Credits'));
    await user.click(link('Issues'));
    expect(url).toHaveTextContent('/issues?state=closed&page=3&page_size=10#history');
    expect(link('Issues')).toHaveFocus();
    await user.click(link('Credits'));
    await user.click(screen.getByRole('button', { name: 'Direct issue link' }));
    expect(url.textContent).toBe('/issues?state=current');
    await user.click(screen.getByRole('button', { name: 'Clear query' }));
    await user.click(link('Calls'));
    expect(link('Issues')).toHaveAttribute('href', '/issues');
  });

  it('keeps personal service selections and drops stored positions across a new account, level, policy or logout', async () => {
    const { user, queryClient } = await renderWithProviders(<Pages group="own" />, {
      station: 'user',
      role: 'level5',
      route: '/endpoints?connection_id=42&connection-credentials_page=2',
    });
    act(() => {
      queryClient.setQueryData(sessionKey, identity('101'));
    });
    const services = () => screen.getByRole('link', { name: 'Services' });
    await user.click(screen.getByRole('link', { name: 'Models' }));
    await waitFor(() =>
      expect(services()).toHaveAttribute(
        'href',
        '/endpoints?connection_id=42&connection-credentials_page=2',
      ),
    );
    await user.click(services());
    act(() => {
      queryClient.setQueryData(sessionKey, identity('202'));
    });
    await waitFor(() => expect(services()).toHaveAttribute('href', '/endpoints'));
    await user.click(screen.getByRole('link', { name: 'Models' }));
    expect(services()).toHaveAttribute('href', '/endpoints');
    for (const other of [identity('202'), identity('101', 6), identity('101', 5, '2')]) {
      act(() => {
        queryClient.setQueryData(sessionKey, other);
      });
      await waitFor(() => expect(services()).toHaveAttribute('href', '/endpoints'));
    }
    act(() => {
      clearManagementSession(queryClient, 'steward');
    });
    expect(services()).toHaveAttribute('href', '/endpoints');
    act(() => {
      queryClient.setQueryData(sessionKey, identity('101'));
    });
    await waitFor(() => expect(services()).toHaveAttribute('href', '/endpoints'));
  });
});
