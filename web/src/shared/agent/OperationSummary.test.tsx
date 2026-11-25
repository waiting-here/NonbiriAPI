import { screen } from '@testing-library/react';
import { expect, it } from 'vitest';
import { renderWithProviders } from '../../../test/unit/support';
import { textDiff } from './presentation';
import { MemoryDiff } from './AgentParts';
import copy from '@shared/i18n/common/en.json';
import { OperationSummary } from './OperationSummary';

it('shows the exact management request before approval', async () => {
  await renderWithProviders(
    <OperationSummary
      operation={{
        kind: 'management.api',
        target_id: '',
        input: {
          route: 'PATCH /admin/api/site-config/{key}',
          path_params: { key: 'site_name' },
          body: { value: 'New site name' },
        },
      }}
      preview={{
        title: 'Update site name',
        description: '',
        dangerous: true,
        requires_elevation: false,
      }}
    />,
    { station: 'admin', role: 'admin' },
  );
  expect(screen.getByText('Update site name')).toBeVisible();
  expect(screen.getByText(/Each request runs independently/)).toBeVisible();
  const request = screen.getByText(/PATCH \/admin\/api\/site-config/);
  expect(request).toBeVisible();
  expect(request).toHaveTextContent('site_name');
  expect(request).toHaveTextContent('New site name');
});

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

it('shows changed grants, credit limits at their exact precision and the required authorization', async () => {
  await renderWithProviders(
    <OperationSummary
      operation={{
        kind: 'users.permissions',
        target_id: '5',
        input: {
          donation_scope: 'full',
          permissions: { 'ai.chat': true, 'users.ban': true, private_marker: false },
          limits: { 'users.balance_adjustment_max_milli': '1234501' },
        },
      }}
      preview={{
        title: '',
        description: '',
        dangerous: true,
        requires_elevation: true,
        before: JSON.stringify({
          donation_scope: 'standard',
          permissions: { 'ai.chat': false, 'users.ban': true },
          limits: { 'users.balance_adjustment_max_milli': '1000' },
        }),
      }}
    />,
    { station: 'admin', role: 'admin' },
  );
  expect(
    screen.getByText(copy.management.permissions.labels.ai_chat).parentElement,
  ).toHaveTextContent('Disabled → Enabled');
  expect(
    screen.getByText(copy.management.permissions.limits.users_balance_adjustment_max_milli)
      .parentElement,
  ).toHaveTextContent('1 credits → 1,234.501 credits');
  expect(
    screen.getByText(copy.management.permissions.scope.title + ':').parentElement,
  ).toHaveTextContent('Standard → Full');
  expect(screen.queryByText(copy.management.permissions.labels.users_ban)).not.toBeInTheDocument();
  expect(screen.queryByText('private_marker')).not.toBeInTheDocument();
  expect(screen.getByText(copy.common.agent.highRiskOperation)).toBeVisible();
  expect(screen.getByText(copy.common.agent.elevationNeeded)).toBeVisible();
});
it('summarizes a donation review using its role-visible receipt and the actual pending scope', async () => {
  const view = await renderWithProviders(
    <OperationSummary
      operation={{
        kind: 'donation.review',
        target_id: '9',
        input: {
          decision: 'approve',
          reason: 'Verified the submitted uses.',
          signature: 'unpublished-signature',
        },
      }}
      preview={{
        title: '',
        description: '',
        dangerous: false,
        requires_elevation: false,
        before: JSON.stringify({
          use_summary: { total: '9', pending: '2' },
          keys: [
            {
              id: '7',
              safe_note: 'Operator-visible note',
              binding_count: '3',
              display_head: 'hidden-key-prefix',
            },
          ],
        }),
      }}
    />,
    { station: 'admin', role: 'admin' },
  );
  expect(screen.getByText(copy.common.agent.operationNames.approveReview)).toBeVisible();
  expect(screen.getByText('Verified the submitted uses.')).toBeVisible();
  expect(screen.getByText('Services covered by this review: 2')).toBeVisible();
  await view.user.click(screen.getByText('Visible donation keys (1)'));
  expect(screen.getByText('Current model bindings: 3')).toBeVisible();
  expect(screen.getByText(/Operator-visible note/)).toBeVisible();
  expect(screen.queryByText(/unpublished-signature|hidden-key-prefix/)).not.toBeInTheDocument();
});
it('summarizes a steward resource adjustment with the target, asset, direction and pinned quantity', async () => {
  await renderWithProviders(
    <OperationSummary
      operation={{
        kind: 'management.api',
        target_id: '',
        input: {
          route: 'PATCH /api/steward/users/{id}/resources',
          path_params: { id: '912' },
          body: {
            asset: 'fish_food',
            direction: 'decrease',
            expected_quantity: '12000',
            quantity: '5',
            reason: 'Inventory correction',
            safe_note: 'unrelated field',
          },
        },
      }}
    />,
    { station: 'admin', role: 'admin' },
  );
  expect(screen.getByText('User ID: 912')).toBeVisible();
  expect(screen.getByText('fish_food')).toBeVisible();
  expect(screen.getByText('12,000')).toBeVisible();
  expect(screen.getByText('Inventory correction')).toBeVisible();
  expect(screen.queryByText('unrelated field')).not.toBeInTheDocument();
});
it('keeps unchanged interior lines and limits very large memory diffs', async () => {
  const old = Array.from({ length: 80 }, (_, i) => String(i));
  const next = [...old];
  next[1] = 'second';
  next[78] = 'penultimate';
  const diff = textDiff(old.join('\n'), next.join('\n'));
  expect(diff.filter((row) => row.kind === 'same')).toHaveLength(78);
  expect(diff.filter((row) => row.kind === 'remove').map((row) => row.text)).toEqual(['1', '78']);
  expect(diff.filter((row) => row.kind === 'add').map((row) => row.text)).toEqual([
    'second',
    'penultimate',
  ]);
  const view = await renderWithProviders(
    <MemoryDiff
      before={Array(550).fill('old').join('\n')}
      after={Array(550).fill('new').join('\n')}
    />,
    { station: 'admin', role: 'admin' },
  );
  expect(view.container.querySelectorAll('.agent-diff span')).toHaveLength(401);
  expect(screen.getByText(/700 additional diff lines omitted/)).toBeVisible();
});

it('retains a single edited line in the middle of a long memory', async () => {
  const old = Array.from({ length: 1000 }, (_, i) => 'line ' + i);
  const next = [...old];
  next[500] = 'updated middle entry';
  await renderWithProviders(<MemoryDiff before={old.join('\n')} after={next.join('\n')} />, {
    station: 'admin',
    role: 'admin',
  });
  expect(screen.getByText(/updated middle entry/)).toBeVisible();
  expect(screen.getByText(/− line 500/)).toBeVisible();
});

it('aligns sparse edits in a long memory without a quadratic comparison table', () => {
  const old = Array.from({ length: 3000 }, (_, i) => String(i));
  const next = [...old];
  next[1] = 'first edit';
  next[2998] = 'last edit';
  const diff = textDiff(old.join('\n'), next.join('\n'));
  expect(diff.filter((line) => line.kind === 'same')).toHaveLength(2998);
  expect(diff.filter((line) => line.kind === 'add').map((line) => line.text)).toEqual([
    'first edit',
    'last edit',
  ]);
});
