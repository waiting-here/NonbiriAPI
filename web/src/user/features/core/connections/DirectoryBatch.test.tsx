import { screen, waitFor, within } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { renderWithProviders } from '../../../../../test/unit/support';
import common from '@shared/i18n/common/en.json';
import { ApiError } from '@shared/query/http';
import type { ConnectionIntent, ConnectionResult } from './resourceOperation';
import { DirectoryBatch } from './DirectoryBatch';
import type { OperationOutcome } from '@shared/operations/useRetainedOperation';
const mocks = vi.hoisted(() => ({ run: vi.fn(), outcome: 'idle' as OperationOutcome }));
afterEach(() => {
  mocks.outcome = 'idle';
});
vi.mock('./ManualCatalog', () => ({
  ManualCatalog: ({ onPick }: { onPick: (entry: object, key: object) => void }) => (
    <button
      type="button"
      onClick={() =>
        onPick(
          { id: '11', note: 'Shared entry', base_url: 'https://provider.example/v1' },
          { id: '21', native_services: ['chat_completions', 'responses'] },
        )
      }
    >
      Choose shared grant
    </button>
  ),
}));
vi.mock('./useConnectionOperation', () => ({
  useConnectionOperation: (_account: string, onSaved: (value: ConnectionResult) => void) => ({
    run: async (intent: ConnectionIntent) => {
      const result = await mocks.run(intent);
      onSaved(result);
      return result;
    },
    outcome: mocks.outcome,
    isPending: false,
    error: null,
    refreshError: null,
    check: async () => undefined,
    reset: () => undefined,
  }),
}));
describe('directory model batch', () => {
  it('applies known directory services to the chosen target and preserves a user override', async () => {
    const view = await renderWithProviders(
      <DirectoryBatch
        accountId="1"
        rows={[
          {
            id: 'known',
            upstream_model_id: 'known-model',
            provider: 'demo',
            native_services: ['chat_completions'],
            service_evidence: 'provider',
            needs_service_selection: false,
          },
        ]}
      />,
      { station: 'user', role: 'user' },
    );
    await view.user.click(
      screen.getByText(common.common.connections.chooseEntry, { selector: 'strong' }),
    );
    await view.user.click(screen.getByRole('button', { name: 'Choose shared grant' }));
    const row = screen.getByText('known-model', { selector: 'strong' }).closest('details')!;
    const chat = within(row).getByRole('checkbox', {
      name: common.common.modelTypes.chat_completions,
    });
    expect(chat).toBeChecked();
    await view.user.click(chat);
    view.rerender(
      <DirectoryBatch
        accountId="1"
        rows={[
          {
            id: 'known',
            upstream_model_id: 'known-model',
            provider: 'demo',
            native_services: ['chat_completions', 'responses'],
            service_evidence: 'provider',
            needs_service_selection: false,
          },
        ]}
      />,
    );
    expect(chat).not.toBeChecked();
    expect(
      within(row).getByRole('checkbox', { name: common.common.modelTypes.responses }),
    ).not.toBeChecked();
  });
  it('uses the same readiness for individual and batch saves when the upstream name is empty', async () => {
    mocks.run.mockClear();
    const c = common.common.connections;
    const view = await renderWithProviders(
      <DirectoryBatch
        accountId="1"
        rows={[
          {
            id: '1',
            upstream_model_id: 'model-a',
            provider: 'demo',
            native_services: [],
            service_evidence: 'unknown',
            needs_service_selection: true,
          },
        ]}
      />,
      { station: 'user', role: 'user' },
    );
    await view.user.click(screen.getByText(c.chooseEntry, { selector: 'strong' }));
    await view.user.click(screen.getByRole('button', { name: 'Choose shared grant' }));
    const row = screen.getByText('model-a', { selector: 'strong' }).closest('details')!;
    await view.user.click(within(row).getByRole('checkbox', { name: 'Shared entry · model-a' }));
    await view.user.click(
      within(row).getByRole('checkbox', { name: common.common.modelTypes.responses }),
    );
    await view.user.click(
      within(row).getByRole('checkbox', { name: common.common.modelTypes.chat_responses }),
    );
    const single = within(row).getByRole('button', { name: c.saveMapping });
    const batch = screen.getByRole('button', {
      name: c.saveSelectedModels.replace('{{count}}', '1'),
    });
    expect(single).toBeEnabled();
    expect(batch).toBeEnabled();
    const upstream = within(row).getByLabelText(c.upstreamModel + ' · Shared entry');
    await view.user.clear(upstream);
    expect(single).toBeDisabled();
    expect(batch).toBeDisabled();
    await view.user.type(upstream, '  ');
    expect(single).toBeDisabled();
    expect(batch).toBeDisabled();
    await view.user.clear(upstream);
    await view.user.type(upstream, 'model-a');
    expect(single).toBeEnabled();
    mocks.outcome = 'refresh-failed';
    view.rerender(
      <DirectoryBatch
        accountId="1"
        rows={[
          {
            id: '1',
            upstream_model_id: 'model-a',
            provider: 'demo',
            native_services: [],
            service_evidence: 'unknown',
            needs_service_selection: true,
          },
        ]}
      />,
    );
    expect(single).toBeDisabled();
    expect(batch).toBeDisabled();
    await view.user.click(single);
    expect(mocks.run).not.toHaveBeenCalled();
  });
  it('requires explicit services and retries only unfinished stages after one model fails', async () => {
    const c = common.common.connections;
    let failed = false;
    mocks.run.mockImplementation(async (intent: ConnectionIntent) => {
      if (intent.kind === 'manual') return { kind: 'manual' };
      if (intent.kind === 'model') {
        if (intent.input.model === 'model-b' && !failed) {
          failed = true;
          throw new ApiError('invalid_request', 'Synthetic model failure', 400);
        }
        return {
          kind: 'model',
          model: {
            id: intent.input.model === 'model-a' ? '51' : '52',
            binding_revision: '1',
            full_name: 'demo/' + intent.input.model,
          },
        };
      }
      return { kind: 'binding' };
    });
    const rows = ['model-a', 'model-b'].map((upstream_model_id, index) => ({
      id: String(index + 1),
      upstream_model_id,
      provider: 'demo',
      native_services: [],
      service_evidence: 'unknown' as const,
      needs_service_selection: true,
    }));
    const view = await renderWithProviders(<DirectoryBatch accountId="1" rows={rows} />, {
      station: 'user',
      role: 'user',
    });
    await view.user.click(screen.getByText(c.chooseEntry, { selector: 'strong' }));
    await view.user.click(screen.getByRole('button', { name: 'Choose shared grant' }));
    const batch = screen.getByRole('button', {
      name: c.saveSelectedModels.replace('{{count}}', '2'),
    });
    expect(batch).toBeDisabled();
    for (const name of ['model-a', 'model-b']) {
      const title = screen.getByText(name, { selector: 'strong' });
      const row = title.closest('details')!;
      await view.user.click(within(row).getByRole('checkbox', { name: 'Shared entry · ' + name }));
      const services = within(row).getByRole('checkbox', {
        name: common.common.modelTypes.responses,
      });
      expect(services).not.toBeChecked();
      await view.user.click(services);
      await view.user.click(
        within(row).getByRole('checkbox', { name: common.common.modelTypes.chat_responses }),
      );
    }
    expect(batch).toBeEnabled();
    await view.user.click(batch);
    const retry = await screen.findByRole('button', {
      name: c.retryUnfinishedModels.replace('{{count}}', '1'),
    });
    expect(screen.getAllByText(c.mappingSaved)).toHaveLength(1);
    await view.user.click(retry);
    await waitFor(() => expect(screen.getAllByText(c.mappingSaved)).toHaveLength(2));
    const intents = mocks.run.mock.calls.map(([intent]) => intent as ConnectionIntent);
    expect(intents.filter((intent) => intent.kind === 'manual')).toHaveLength(2);
    expect(
      intents.filter((intent) => intent.kind === 'manual').every((intent) => intent.reuseExisting),
    ).toBe(true);
    expect(
      intents.filter((intent) => intent.kind === 'model' && intent.input.model === 'model-a'),
    ).toHaveLength(1);
    expect(
      intents.filter((intent) => intent.kind === 'model' && intent.input.model === 'model-b'),
    ).toHaveLength(2);
    const bindings = intents.filter((intent) => intent.kind === 'binding');
    expect(bindings).toHaveLength(2);
    expect(bindings[0]).toMatchObject({
      modelId: '51',
      selection: {
        targets: [
          { endpoint_key_id: '21', upstream_model_id: 'model-a', native_services: ['responses'] },
        ],
      },
    });
  });
});
