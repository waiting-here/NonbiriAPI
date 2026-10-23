vi.mock('@shared/components/AsyncLeaveDialog', () => ({ AsyncLeaveDialog: () => null }));
import { useEffect } from 'react';
import { useQuery } from '@tanstack/react-query';
import { act, fireEvent, screen, waitFor } from '@testing-library/react';
import { expect, it, vi } from 'vitest';
import { fetchTimeContext, timeContextQueryKey } from '@shared/time';
import type { CharityRole } from '@shared/operations/charity';
import { renderWithProviders } from '../../../../test/unit/support';
import { ModelsPanel } from './CharityModels';

const json = (body: unknown) =>
  new Response(JSON.stringify(body), { headers: { 'Content-Type': 'application/json' } });

function EditDuringTimeHydration({
  role,
  field,
}: {
  role: CharityRole;
  field: 'Provider' | 'Model';
}) {
  const context = useQuery({
    queryKey: timeContextQueryKey(role),
    queryFn: ({ signal }) => fetchTimeContext(role, signal),
    staleTime: 30_000,
  });
  useEffect(() => {
    if (context.isSuccess) {
      // Deliver an ordinary field edit in the same commit as date hydration.
      fireEvent.change(screen.getByLabelText(field), { target: { value: 'headers' } });
    }
  }, [context.isSuccess, field]);
  return (
    <ModelsPanel
      role={role}
      accountId="1"
      renderKeyManager={() => null}
      renderScopedKeys={() => null}
    />
  );
}

it.each([
  ['admin', 'Provider'],
  ['admin', 'Model'],
  ['steward', 'Provider'],
  ['steward', 'Model'],
] as const)(
  'retains nullable discount dates when a %s %s edit overlaps time hydration',
  async (role, field) => {
    let complete!: (response: Response) => void;
    vi.stubGlobal(
      'fetch',
      vi.fn((path: string) => {
        if (path.endsWith('/time-context'))
          return new Promise<Response>((resolve) => {
            complete = resolve;
          });
        throw new Error('Unexpected request: ' + path);
      }),
    );
    const view = await renderWithProviders(<EditDuringTimeHydration role={role} field={field} />, {
      station: role === 'admin' ? 'admin' : 'user',
      role: role === 'admin' ? 'admin' : 'level6',
      route: '/charity?charity_section=models&model_workspace=new',
    });
    fireEvent.change(screen.getByLabelText(field === 'Provider' ? 'Model' : 'Provider'), {
      target: { value: 'example' },
    });
    await act(async () => complete(json({ mode: 'site', offset_minutes: 480 })));
    await waitFor(() => expect(screen.getByLabelText(field)).toHaveValue('headers'));
    await waitFor(() =>
      expect(screen.getByRole('button', { name: 'Add charity model' })).toBeEnabled(),
    );
    expect(view.container.querySelector('.field-error')).not.toBeInTheDocument();
    for (const input of view.container.querySelectorAll('input[type="datetime-local"]')) {
      expect(input).toBeEnabled();
      expect(input).toHaveValue('');
    }
  },
);
