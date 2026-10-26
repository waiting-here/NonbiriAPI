import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { screen } from '@testing-library/react';
import { expect, it, vi } from 'vitest';
import { renderWithProviders } from '../../../../../test/unit/support';
import { ServiceModelSetup } from './ServiceModelSetup';
import type { DiscoverySource } from './types';

vi.mock('./SourceModelMapping', () => ({
  SourceModelMapping: () => <button>Configure retained model</button>,
}));
vi.mock('./OperationNotice', () => ({ ConnectionOperationNotice: () => null }));
vi.mock('./useConnectionOperation', () => ({
  useConnectionOperation: () => ({ isPending: false, outcome: 'idle' }),
}));

it('shows a failed catalog refresh without blocking retained model configuration', async () => {
  const source: DiscoverySource = {
    id: '1',
    name: '',
    connection_id: '1',
    base_url: 'https://example.test/v1',
    list_format: 'openai',
    models_url: 'https://example.test/v1/models',
    credential_id: '1',
    credential_version: '1',
    revision: '1',
    discovery_source_revision: '1',
    enabled: true,
    state: 'failed',
    checked_at: 1,
    safe_class: 'unavailable',
    safe_diag: 'upstream returned status 502',
    created_at: 1,
    updated_at: 1,
  };
  const entry = JSON.parse(
    readFileSync(resolve('../internal/resources/testdata/endpoint.json'), 'utf8'),
  );
  const entryKey = JSON.parse(
    readFileSync(resolve('../internal/resources/testdata/endpoint_key.json'), 'utf8'),
  );
  await renderWithProviders(
    <ServiceModelSetup accountId="1" entry={entry} entryKey={entryKey} source={source} />,
    { station: 'user', role: 'user', locale: 'en' },
  );
  expect(screen.getByText('Could not load the model list')).toBeVisible();
  expect(
    screen.getByText('The upstream service is temporarily unavailable. Retry shortly.'),
  ).toBeVisible();
  expect(screen.getByText('upstream returned status 502')).toBeVisible();
  expect(screen.getByRole('button', { name: 'Refresh catalog' })).toBeEnabled();
  expect(screen.getByRole('button', { name: 'Configure retained model' })).toBeEnabled();
});
