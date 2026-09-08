import { screen, waitFor, within } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import {
  installJsonFetchFixtures,
  renderWithProviders,
  type JsonFetchFixture,
} from '../../../../test/unit/support';
import { RoleLogPanel } from './RoleLogPanel';

const usage = {
  uncached_input_tokens: '0',
  cache_write_input_tokens: '0',
  cache_read_input_tokens: '0',
  output_tokens: '1',
  total_tokens: '1',
  usage_unknown: false,
  charge: '0',
};
const requestID = `req_${'A'.repeat(21)}Q`;

const stewardRow = (caller_identity: unknown) => ({
  id: requestID,
  route_kind: 'charity_chat_completions',
  caller_result_class: 'success',
  caller_status: 200,
  caller_error_code: null,
  started_at: 1,
  completed_at: 2,
  usage,
  caller_identity,
  attempt_count: '1',
});

function installFixtures(
  role: 'admin' | 'steward' | 'user',
  row: Record<string, unknown>,
  detail?: unknown,
) {
  const prefix = role === 'admin' ? '/admin/api' : '/api';
  const logPath =
    role === 'admin' ? '/admin/api/logs' : role === 'user' ? '/api/logs' : '/api/steward/logs';
  const pageBody = {
    data: [row],
    next_cursor: null,
    pagination: { page: '1', page_size: 20, total_items: '1', total_pages: '1' },
  };
  const fixtures: JsonFetchFixture[] = [
    {
      method: 'GET',
      path: `${prefix}/time-zones`,
      body: { version: 'go1.26.6-zoneinfo', zones: ['UTC'] },
    },
    {
      method: 'GET',
      path: `${logPath}?page=1&page_size=20`,
      body: pageBody,
    },
  ];
  if (detail !== undefined) {
    fixtures.push({
      method: 'GET',
      path: `${logPath}/${encodeURIComponent(requestID)}?attempt_page=1&attempt_page_size=20`,
      body: {
        ...(detail as Record<string, unknown>),
        attempt_pagination: { page: '1', page_size: 20, total_items: '0', total_pages: '1' },
      },
    });
  }
  return installJsonFetchFixtures(fixtures);
}

function installClipboard(writeText: ReturnType<typeof vi.fn>) {
  Object.defineProperty(navigator, 'clipboard', {
    configurable: true,
    value: { writeText },
  });
  return writeText;
}

afterEach(() => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

describe('steward caller identity', () => {
  it('shows the complete identity in the list and detail and copies the full ID', async () => {
    const discordID = '1'.repeat(18);
    const row = stewardRow({ discord_nickname: 'Ada Example', discord_id: discordID });
    const fetchMock = installFixtures('steward', row, {
      request: row,
      attempts: { data: [], next_cursor: null },
    });
    const writeText = vi.fn().mockResolvedValue(undefined);
    const view = await renderWithProviders(<RoleLogPanel accountId="viewer" role="steward" />, {
      station: 'user',
      role: 'level5',
    });
    installClipboard(writeText);

    await waitFor(() =>
      expect(fetchMock.mock.calls.map(([path]) => String(path))).toContain(
        '/api/steward/logs?page=1&page_size=20',
      ),
    );
    await waitFor(() => expect(screen.getByText('Ada Example', { exact: true })).toBeVisible());
    expect(screen.getByText(discordID, { exact: true })).toBeVisible();
    const copy = screen.getByRole('button', { name: 'Copy Discord ID' });
    expect(navigator.clipboard.writeText).toBe(writeText);
    await view.user.click(copy);
    await waitFor(() => expect(writeText).toHaveBeenCalledWith(discordID));
    await waitFor(() => expect(copy).toHaveTextContent('Copied'));

    await view.user.click(screen.getByRole('button', { name: 'Details' }));
    const dialog = await screen.findByRole('dialog');
    await waitFor(() =>
      expect(within(dialog).getByText('Ada Example', { exact: true })).toBeVisible(),
    );
    expect(within(dialog).getByText(discordID, { exact: true })).toBeVisible();
    expect(fetchMock.mock.calls.map(([path]) => String(path))).toContain(
      `/api/steward/logs/${requestID}?attempt_page=1&attempt_page_size=20`,
    );
  });

  it('keeps partial identities explainable and offers copy for an available ID', async () => {
    const discordID = '1'.repeat(18);
    const partialFetch = installFixtures(
      'steward',
      stewardRow({ discord_nickname: null, discord_id: discordID }),
    );
    await renderWithProviders(<RoleLogPanel accountId="viewer" role="steward" />, {
      station: 'user',
      role: 'level5',
    });
    await waitFor(() => expect(partialFetch.mock.calls.length).toBeGreaterThan(0));
    await waitFor(() =>
      expect(screen.getByText('Profile unavailable', { exact: true })).toBeVisible(),
    );
    expect(screen.getByText(discordID, { exact: true })).toBeVisible();
    expect(screen.getByRole('button', { name: 'Copy Discord ID' })).toBeVisible();
  });

  it('shows detached identities as unavailable without a copy button', async () => {
    const detachedFetch = installFixtures('steward', stewardRow(null));
    await renderWithProviders(<RoleLogPanel accountId="viewer" role="steward" />, {
      station: 'user',
      role: 'level5',
    });
    await waitFor(() => expect(detachedFetch.mock.calls.length).toBeGreaterThan(0));
    await waitFor(() =>
      expect(screen.getByText('Profile unavailable', { exact: true })).toBeVisible(),
    );
    expect(screen.queryByRole('button', { name: 'Copy Discord ID' })).toBeNull();
  });

  it('reports clipboard failure after the promise rejects', async () => {
    const discordID = '1'.repeat(18);
    const fetchMock = installFixtures(
      'steward',
      stewardRow({ discord_nickname: 'Ada Example', discord_id: discordID }),
    );
    const writeText = vi.fn().mockRejectedValue(new Error('clipboard unavailable'));
    const view = await renderWithProviders(<RoleLogPanel accountId="viewer" role="steward" />, {
      station: 'user',
      role: 'level5',
    });
    installClipboard(writeText);
    Object.defineProperty(document, 'execCommand', {
      configurable: true,
      value: vi.fn().mockReturnValue(false),
    });
    await waitFor(() => expect(fetchMock.mock.calls.length).toBeGreaterThan(0));
    const copy = await screen.findByRole('button', { name: 'Copy Discord ID' });
    await view.user.click(copy);
    await waitFor(() => expect(screen.getByRole('status')).toHaveTextContent('Copy failed'));
    expect(writeText).toHaveBeenCalledWith(discordID);
  });

  it('keeps long caller values visible for narrow-screen wrapping', async () => {
    const nickname = '名'.repeat(80);
    const discordID = '9'.repeat(128);
    const fetchMock = installFixtures(
      'steward',
      stewardRow({ discord_nickname: nickname, discord_id: discordID }),
    );
    await renderWithProviders(<RoleLogPanel accountId="viewer" role="steward" />, {
      station: 'user',
      role: 'level5',
    });
    await waitFor(() => expect(fetchMock.mock.calls.length).toBeGreaterThan(0));
    await waitFor(() => expect(screen.getByText(nickname, { exact: true })).toBeVisible());
    expect(screen.getByText(discordID, { exact: true })).toBeVisible();
    expect(
      screen.getByText(nickname, { exact: true }).closest('.log-caller-identity'),
    ).not.toBeNull();
  });

  it.each([
    {
      role: 'admin' as const,
      station: 'admin' as const,
      testRole: 'admin' as const,
      row: {
        id: requestID,
        route_kind: 'openai_chat_completions',
        caller_result_class: 'success',
        caller_status: 200,
        caller_error_code: null,
        started_at: 1,
        completed_at: 2,
        usage,
        user_id: null,
        attempt_count: '1',
      },
    },
    {
      role: 'user' as const,
      station: 'user' as const,
      testRole: 'user' as const,
      row: {
        id: requestID,
        route_kind: 'openai_chat_completions',
        caller_result_class: 'success',
        caller_status: 200,
        caller_error_code: null,
        started_at: 1,
        completed_at: 2,
        usage,
        model: 'model',
        attempt_count: '1',
      },
    },
  ])(
    'does not render a caller block for the $role station',
    async ({ role, station, testRole, row }) => {
      const fetchMock = installFixtures(role, row);
      await renderWithProviders(<RoleLogPanel accountId="viewer" role={role} />, {
        station,
        role: testRole,
      });
      await waitFor(() => expect(fetchMock.mock.calls.length).toBeGreaterThan(0));
      await waitFor(() => expect(screen.getByRole('button', { name: 'Details' })).toBeVisible());
      expect(screen.queryByText('Caller identity', { exact: true })).toBeNull();
      expect(screen.queryByRole('button', { name: 'Copy Discord ID' })).toBeNull();
    },
  );
});
