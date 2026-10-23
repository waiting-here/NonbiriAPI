import { screen, waitFor, within } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { renderWithProviders } from '../../../../../test/unit/support';
import common from '@shared/i18n/common/en.json';
import { ApiError } from '@shared/query/http';
import type { ConnectionIntent, ConnectionResult } from './resourceOperation';
import { DirectoryBatch } from './DirectoryBatch';
const mocks = vi.hoisted(() => ({ run: vi.fn() }));
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
    outcome: 'idle',
    isPending: false,
    error: null,
    refreshError: null,
    check: async () => undefined,
    reset: () => undefined,
  }),
}));
describe('directory model batch', () => {
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
