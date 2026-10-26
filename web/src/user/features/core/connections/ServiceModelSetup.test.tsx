import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { screen } from '@testing-library/react';
import { expect, it, vi } from 'vitest';
import { renderWithProviders } from '../../../../../test/unit/support';
import { ServiceModelSetup } from './ServiceModelSetup';

const fixture = (name: string) =>
  JSON.parse(readFileSync(resolve('../internal/resources/testdata/' + name), 'utf8'));
const catalog = {
  evidence: {
    state: 'failed',
    revision: '3',
    result: 'nonempty',
    safe_class: 'unavailable',
    observed_at: 1,
    count: '1',
    last_success_at: 1,
  },
  automatic_entries: [
    { id: '42', upstream_model_id: 'another-model', provider: '', source_type: 'automatic' },
    { id: '41', upstream_model_id: 'retained-model', provider: '', source_type: 'automatic' },
  ],
  manual_entries: [],
  pagination: { page: '1', page_size: 20, total_items: '1', total_pages: '1' },
};
const run = vi.fn().mockResolvedValue(undefined);
vi.mock('./api', () => ({ connectionAPI: { models: vi.fn().mockResolvedValue({ data: [] }) } }));
vi.mock('../numberedQueries', () => ({
  useNumberedCatalog: () => ({ data: catalog, isPending: false, isFetching: false }),
}));
vi.mock('../EndpointDetail', () => ({ PairManualCatalog: () => <p>Manual catalog editor</p> }));
vi.mock('./DirectoryBatch', () => ({
  DirectoryBatch: () => (
    <div>
      <p>Selected model preview</p>
      <input aria-label="Draft model name" defaultValue="" />
    </div>
  ),
}));
vi.mock('./OperationNotice', () => ({ ConnectionOperationNotice: () => null }));
vi.mock('./useConnectionOperation', () => ({
  useConnectionOperation: () => ({ isPending: false, outcome: 'idle', run }),
}));

it('keeps retained models and manual maintenance reachable after a failed fetch', async () => {
  const view = await renderWithProviders(
    <ServiceModelSetup
      accountId="1"
      entry={fixture('endpoint.json')}
      entryKey={fixture('endpoint_key.json')}
    />,
    { station: 'user', role: 'user', locale: 'en' },
  );
  expect(screen.getByText('Could not load the model list')).toBeVisible();
  expect(screen.getByText(/last successful list and manual models are kept/)).toBeVisible();
  expect(screen.getByRole('button', { name: 'Fetch models' })).toBeEnabled();
  await view.user.click(screen.getByLabelText('retained-model'));
  expect(screen.getByText('Selected model preview')).toBeVisible();
  await view.user.type(screen.getByLabelText('Draft model name'), 'Keep my edits');
  await view.user.click(screen.getByLabelText('another-model'));
  expect(screen.getByLabelText('Draft model name')).toHaveValue('Keep my edits');
  await view.user.click(screen.getByText('Manual models', { selector: 'strong' }));
  expect(screen.getByText('Manual catalog editor')).toBeVisible();
});
