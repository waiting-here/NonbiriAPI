import { screen } from '@testing-library/react';
import { expect, it } from 'vitest';
import { renderWithProviders } from '../../../test/unit/support';
import { OperationSummary } from './OperationSummary';

it('shows the fixed cleanup count and filters before approving a management cleanup', async () => {
  await renderWithProviders(
    <OperationSummary
      operation={{
        kind: 'management.cleanup',
        target_id: '',
        input: { filter: { source: 'ai_manual' }, cutoff_max_id: '321' },
      }}
      preview={{
        title: '清理管理操作记录',
        description: '',
        dangerous: true,
        requires_elevation: false,
        before: JSON.stringify({
          all: false,
          filter: { source: 'ai_manual' },
          cutoff_max_id: '321',
          matching_operations: '1200',
        }),
      }}
    />,
    { station: 'admin', role: 'admin' },
  );
  expect(screen.getByText('Clear management history')).toBeVisible();
  expect(
    screen.getByText('This scope contains 1,200 operations through record 321.'),
  ).toBeVisible();
  expect(screen.getByText('Authorized AI Agent operation')).toBeVisible();
});

it('identifies a preset target as a donation and shows its saved mappings and skip rules', async () => {
  const view = await renderWithProviders(
    <OperationSummary
      operation={{
        kind: 'charity.binding.apply_preset',
        target_id: '42',
        input: { channel_id: 'mch_example' },
      }}
      preview={{
        title: 'Apply preset',
        description: '',
        dangerous: false,
        requires_elevation: false,
        before: JSON.stringify({
          channel: { name: 'Example channel' },
          preset: {
            mappings: [
              {
                upstream_model_id: 'upstream-example',
                charity_model_id: '9',
                full_name: '[公益]example/model',
              },
            ],
          },
        }),
      }}
    />,
    { station: 'admin', role: 'admin' },
  );
  expect(screen.getByText('Apply channel binding preset')).toBeVisible();
  expect(screen.getByText('Donation ID: 42')).toBeVisible();
  expect(screen.queryByText('Model ID: 42')).not.toBeInTheDocument();
  expect(screen.getByText('Example channel')).toBeVisible();
  expect(
    screen.getByText(/Missing upstream models and existing bindings are skipped/),
  ).toBeVisible();
  await view.user.click(screen.getByText('View preset mappings (1)'));
  expect(screen.getByText('upstream-example → [公益]example/model')).toBeVisible();
});
