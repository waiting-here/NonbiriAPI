import { screen, waitFor, within } from '@testing-library/react';
import type { ReactNode } from 'react';
import { describe, expect, it, vi } from 'vitest';
import { useAdminSession } from '../../admin/data';
import { renderWithProviders } from '../../../test/unit/support';
import en from '../i18n/common/en.json';
import zh from '../i18n/common/zh.json';
import { UserResources } from './UserResources';
import { resourceAdjustmentPreview, type ManagedResource } from '../operations/managedResources';

const copy = en.management.resources;
const resource: ManagedResource = {
  id: 'lake:coins',
  name: '垂钓金币',
  name_en: 'Lake coins',
  group: 'lake',
  precision: 0,
  quantity: '9007199254740992',
  maximum: '9007199254740991',
  capacity: '340282366920938463463374607431768211455',
  minimum: '0',
};

function Session({ children }: { children: ReactNode }) {
  return useAdminSession().data ? children : null;
}

function server(loseFirst = false, additional: ManagedResource[] = []) {
  const writes: { key: string | null; body: Record<string, unknown> }[] = [];
  let current = { ...resource };
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL, options?: RequestInit) => {
      const url = new URL(input instanceof Request ? input.url : String(input), location.origin);
      const reply = (value: unknown) =>
        new Response(JSON.stringify(value), { headers: { 'Content-Type': 'application/json' } });
      if (url.pathname === '/admin/api/session')
        return reply({ admin: { username: 'fixture-admin' } });
      if (url.pathname !== '/admin/api/users/51/resources')
        throw new Error(`Unexpected request: ${url.pathname}`);
      if (options?.method === 'PATCH') {
        const body = JSON.parse(String(options.body)) as Record<string, unknown>;
        writes.push({ key: new Headers(options.headers).get('Idempotency-Key'), body });
        if (loseFirst && writes.length === 1) throw new TypeError('Lost response');
        current = { ...current, quantity: '9007199254740993' };
        return reply({
          resources: [current],
          changes: [{ field: 'lake:coins', before: resource.quantity, after: current.quantity }],
        });
      }
      return reply({ resources: [current, ...additional] });
    }),
  );
  return writes;
}

describe('managed resources', () => {
  it.each(['en', 'zh'] as const)('filters both Whale Haven currencies in %s', async (locale) => {
    const whaleResources = [
      ['whale:pearls', '小鲸币', 'Whale coins'],
      ['whale:conch_shards', '海螺碎片', 'Conch shards'],
    ].map(([id, name, name_en]) => ({
      ...resource,
      id,
      name,
      name_en,
      group: 'whale',
      quantity: '0',
      capacity: '9000000000000',
      maximum: '9000000000000',
    }));
    server(false, whaleResources);
    const { user } = await renderWithProviders(
      <Session>
        <UserResources role="admin" account="fixture-admin" userId="51" editable />
      </Session>,
      { station: 'admin', locale },
    );
    const labels = (locale === 'en' ? en : zh).management.resources;
    const group = await screen.findByRole('combobox', { name: labels.group });
    expect(within(group).getByRole('option', { name: labels.groups.whale })).toHaveValue('whale');
    await user.selectOptions(group, 'whale');
    for (const asset of whaleResources)
      expect(screen.getByText(locale === 'en' ? asset.name_en : asset.name)).toBeVisible();
    expect(screen.queryByText(locale === 'en' ? 'Lake coins' : '垂钓金币')).toBeNull();
  });

  it('shows inventory without adjustment controls when read only', async () => {
    const writes = server();
    await renderWithProviders(
      <Session>
        <UserResources role="admin" account="fixture-admin" userId="51" editable={false} />
      </Session>,
      { station: 'admin', locale: 'en' },
    );
    expect(await screen.findByText(resource.quantity)).toBeVisible();
    expect(screen.getByText(copy.readOnly)).toBeVisible();
    expect(screen.queryByRole('button', { name: copy.adjust })).not.toBeInTheDocument();
    expect(writes).toHaveLength(0);
  });

  it('previews wide integers exactly and retries an uncertain intent with its original key', async () => {
    const writes = server(true);
    const { user } = await renderWithProviders(
      <Session>
        <UserResources role="admin" account="fixture-admin" userId="51" editable />
      </Session>,
      { station: 'admin', locale: 'en' },
    );
    await user.click(await screen.findByRole('button', { name: copy.adjust }));
    await user.type(screen.getByRole('textbox', { name: copy.quantity }), '1');
    await user.type(screen.getByRole('textbox', { name: copy.reason }), 'Missing event grant');
    await user.click(screen.getByRole('button', { name: copy.review }));
    const confirm = screen.getByRole('alertdialog');
    expect(within(confirm).getByText(/9007199254740992 → 9007199254740993/)).toBeVisible();
    expect(writes).toHaveLength(0);
    await user.click(within(confirm).getByRole('button', { name: copy.confirm }));
    await screen.findByText(en.common.outcome.unknownTitle);
    expect(screen.getByRole('textbox', { name: copy.quantity })).toBeDisabled();
    await user.click(screen.getByRole('button', { name: en.common.outcome.recheck }));
    await waitFor(() => expect(writes).toHaveLength(2));
    expect(writes[1]).toEqual(writes[0]);
    expect(writes[0].body).toMatchObject({
      expected_quantity: resource.quantity,
      quantity: '1',
      reason: 'Missing event grant',
    });
    await screen.findByText(en.common.outcome.saved);
  });

  it('rejects fractional, zero, overflow and unavailable quantities before confirmation', () => {
    for (const quantity of ['0', '01', '-1', '1.5', '1e3', '9007199254740992'])
      expect(resourceAdjustmentPreview(resource, 'increase', quantity)).toBeNull();
    expect(resourceAdjustmentPreview({ ...resource, quantity: '0' }, 'decrease', '1')).toBeNull();
    expect(resourceAdjustmentPreview(resource, 'increase', '1')).toBe('9007199254740993');
    expect(
      resourceAdjustmentPreview({ ...resource, quantity: resource.capacity }, 'increase', '1'),
    ).toBeNull();
  });
});
