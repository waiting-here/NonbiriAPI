import { fireEvent, screen, waitFor, within } from '@testing-library/react';
import { describe, expect, test, vi, type Mock } from 'vitest';
import { SettingsPage } from '../../src/admin/pages/SettingsPage';
import { GamesPage } from '../../src/admin/pages/GamesPage';
import { UsersPage } from '../../src/admin/pages/UsersPage';
import { normalizeAdminUser } from '../../src/admin/data';
import {
  normalizeSiteConfigCatalogEntry,
  type SiteConfigCatalogEntry,
} from '../../src/admin/features/operations/core';
import {
  normalizeActiveCounts,
  normalizeGamesConfig,
  type ActiveCounts,
  type GamesConfig,
} from '../../src/admin/features/operations/economy';
import {
  normalizeCharityModel,
  normalizeDonationKey,
  normalizeDonation,
  normalizeEndpointKey,
  normalizeUpstreamModel,
  normalizePlatformModel,
  normalizeUserSummary,
} from '../../src/user/data';
import { normalizeEndpoint as normalizeCoreEndpoint } from '../../src/user/features/core/normalizers';
import {
  normalizeManagementCharityModel,
  normalizeManagementDonation,
  normalizeManagementDonationKey,
} from '../../src/shared/charityManagement';
import { HomePage } from '../../src/user/pages/HomePage';
import { CharityPage } from '../../src/user/pages/CharityPage';
import { installJsonFetchFixtures, renderWithProviders } from './support';
import { positiveDecimalIDNumber } from '../../src/shared/query/normalize';

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'content-type': 'application/json', 'cache-control': 'no-store' },
  });
}

function catalogEntry(
  key: string,
  overrides: Partial<SiteConfigCatalogEntry> = {},
): SiteConfigCatalogEntry {
  return {
    key,
    group: 'fixture',
    type: 'integer',
    title: {
      zh: `${key} 中文`,
      en:
        key === 'anthropic_default_max_tokens'
          ? 'Default Anthropic max output tokens'
          : 'Site timezone offset',
    },
    description: { zh: '用途', en: 'Purpose' },
    unit: { zh: '分钟', en: 'minutes' },
    nullable: false,
    null_writable: false,
    raw_default: 0,
    effective_fallback: 0,
    minimum: -720,
    maximum: 840,
    step: 1,
    allowed_values: [],
    zero_semantics: { zh: '零语义', en: 'Zero semantics' },
    null_semantics: { zh: '空语义', en: 'Null semantics' },
    empty_semantics: { zh: '空字符串语义', en: 'Empty semantics' },
    independent_gates: [],
    write_endpoint: `/admin/api/site-config/${key}`,
    ...overrides,
  };
}

interface SiteConfigSnapshot {
  revision: string;
  values: Record<string, string | number | boolean | null>;
}

function installSiteConfigServer(
  initial: SiteConfigSnapshot,
  catalog: readonly SiteConfigCatalogEntry[],
  catalogResponse: unknown = { data: catalog },
): {
  fetchMock: Mock;
  state: SiteConfigSnapshot;
  patches: Array<{ path: string; value: unknown }>;
} {
  const state = structuredClone(initial);
  const patches: Array<{ path: string; value: unknown }> = [];
  const fetchMock = vi.fn(async (input: string | URL | Request, init?: RequestInit) => {
    const path = new URL(
      input instanceof Request ? input.url : String(input),
      window.location.origin,
    ).pathname;
    const method = (
      init?.method ?? (input instanceof Request ? input.method : 'GET')
    ).toUpperCase();
    if (method === 'GET' && path === '/admin/api/site-config') return jsonResponse(state);
    if (method === 'GET' && path === '/admin/api/site-config/catalog') {
      return jsonResponse(catalogResponse);
    }
    if (method === 'GET' && path === '/admin/api/maintenance') {
      return jsonResponse({ enabled: false, revision: '1' });
    }
    if (method === 'GET' && path === '/admin/api/legal-holds') {
      return jsonResponse({ data: [], next_cursor: null });
    }
    if (method === 'PATCH' && path === '/admin/api/site-config') {
      const body = JSON.parse(String(init?.body)) as {
        expected_revision: string;
        values: SiteConfigSnapshot['values'];
      };
      expect(body.expected_revision).toBe(state.revision);
      for (const [key, value] of Object.entries(body.values)) {
        patches.push({ path: `${path}/${key}`, value });
        state.values[key] = value;
      }
      state.revision = String(BigInt(state.revision) + 1n);
      return jsonResponse({ changed_keys: Object.keys(body.values), revision: state.revision });
    }
    throw new Error(`Unexpected fixture request: ${method} ${path}`);
  });
  vi.stubGlobal('fetch', fetchMock);
  return { fetchMock, state, patches };
}

const coreBaseUser = {
  id: '7',
  username: 'fixture-user',
  avatar: null,
  avatar_url: null,
  guild_nick: null,
  guild_avatar_url: null,
  lang: 'en',
  is_banned: false,
  banned_until: null,
  charity_suspended_until: null,
  endpoint_limit: null,
  effective_endpoint_limit: '4',
  rpm_limit: null,
  effective_rpm_limit: '60',
  concurrency_limit: null,
  effective_concurrency_limit: '5',
  balance: '1',
  donation_credit: '2',
  effective_level: 2,
  level_display_name: 'Lv2',
  game_profile_public: false,
  created_at: 1_700_000_000,
  updated_at: 1_700_000_001,
  usage: {
    total_requests: '1',
    total_uncached_input_tokens: '2',
    total_cache_write_input_tokens: '0',
    total_cache_read_input_tokens: '0',
    total_output_tokens: '3',
    total_prompt_tokens: '2',
    total_completion_tokens: '3',
    total_unknown_usage_requests: '0',
  },
};

describe('screenshot-facing configuration pages', () => {
  test('U1 removes only the internal level-computation implementation hint', async () => {
    installJsonFetchFixtures([
      { method: 'GET', path: '/api/session', body: { user: coreBaseUser } },
      { method: 'GET', path: '/api/me', body: { user: coreBaseUser } },
      {
        method: 'GET',
        path: '/api/me/usage',
        body: {
          total_requests: 1,
          total_prompt_tokens: 2,
          total_completion_tokens: 3,
          total_unknown_usage_requests: 0,
        },
      },
      { method: 'GET', path: '/api/checkin', body: { enabled: false } },
    ]);
    await renderWithProviders(<HomePage />, { station: 'user', locale: 'en', role: 'user' });

    expect(await screen.findByText('Level')).toBeVisible();
    expect(screen.getByText('Lv2', { exact: true })).toBeVisible();
    expect(screen.queryByText(/This page computes nothing/i)).not.toBeInTheDocument();
  });

  test('U2 replaces the large call-status guide with a persistent neutral upstream warning', async () => {
    installJsonFetchFixtures([
      { method: 'GET', path: '/api/session', body: { user: coreBaseUser } },
      {
        method: 'GET',
        path: '/api/charity/models',
        body: {
          state: 'no_models',
          models: [],
          donation_intake: 'closed',
          server_now: 1_788_100_000,
        },
      },
      { method: 'GET', path: '/api/donations', body: [] },
      { method: 'GET', path: '/api/endpoints', body: [] },
    ]);
    await renderWithProviders(<CharityPage />, { station: 'user', locale: 'en', role: 'user' });

    const warning = await screen.findByRole('note');
    expect(warning).toHaveTextContent('Third-party service privacy:');
    expect(warning).toHaveTextContent(/account logs may see the full request content/i);
    expect(warning).toHaveTextContent(/outside this site's control/i);
    expect(warning).toHaveTextContent(
      /availability, response speed, or whether the model used matches the model advertised/i,
    );
    expect(screen.queryByText('Call status guide')).not.toBeInTheDocument();
  });
});

describe('authoritative site-config frontend', () => {
  async function renderSettings() {
    const rendered = await renderWithProviders(<SettingsPage />, {
      station: 'admin',
      locale: 'en',
      role: 'admin',
    });
    rendered.queryClient.setQueryData(['admin', 'session'], {
      admin: { username: 'fixture-admin' },
    });
    return rendered;
  }

  test('keeps edits across groups and searches, then sends one atomic configuration update', async () => {
    const siteName = catalogEntry('site_name', {
      group: 'identity',
      type: 'string',
      title: { zh: '站点名称', en: 'Site name' },
      minimum: 1,
      maximum: 256,
      step: null,
    });
    const limit = catalogEntry('default_endpoint_limit', {
      group: 'limits',
      title: { zh: '端点数量上限', en: 'Endpoint limit' },
      minimum: 1,
      maximum: 10000,
    });
    const server = installSiteConfigServer(
      { revision: '12', values: { site_name: 'Before', default_endpoint_limit: 4 } },
      [siteName, limit],
    );
    const rendered = await renderSettings();
    await rendered.user.click(
      (await screen.findByText('Identity and appearance')).closest('button')!,
    );
    fireEvent.change(screen.getByLabelText('Site name'), { target: { value: 'After' } });
    await rendered.user.click(screen.getByText('Identity and appearance').closest('button')!);
    fireEvent.change(screen.getByLabelText('Search'), { target: { value: 'Endpoint' } });
    await rendered.user.click(screen.getByText('Limits').closest('button')!);
    fireEvent.change(screen.getByLabelText('Endpoint limit'), { target: { value: '8' } });
    fireEvent.change(screen.getByLabelText('Search'), { target: { value: '' } });
    await rendered.user.click(screen.getByText('Identity and appearance').closest('button')!);
    expect(screen.getByLabelText('Site name')).toHaveValue('After');
    await rendered.user.click(screen.getByRole('button', { name: 'Save all changes' }));
    await waitFor(() => expect(server.state.revision).toBe('13'));
    const writes = server.fetchMock.mock.calls.filter(([, init]) => init?.method === 'PATCH');
    expect(writes).toHaveLength(1);
    expect(JSON.parse(String(writes[0][1]?.body))).toEqual({
      expected_revision: '12',
      values: { site_name: 'After', default_endpoint_limit: 8 },
    });
    await waitFor(() =>
      expect(screen.getByRole('button', { name: 'Save all changes' })).toBeDisabled(),
    );
  });
  test('rejects non-canonical Anthropic integers and enforces the frozen timezone step', async () => {
    const anthropic = catalogEntry('anthropic_default_max_tokens', {
      type: 'integer',
      nullable: true,
      null_writable: true,
      raw_default: null,
      effective_fallback: 65_536,
      minimum: 1,
      maximum: 2_147_483_647,
      step: 1,
      unit: { zh: 'Token', en: 'tokens' },
      null_semantics: { zh: '使用内建值', en: 'Use built-in 65536' },
      empty_semantics: { zh: '留空重置', en: 'Empty form sends JSON null and resets the override' },
    });
    const timezone = catalogEntry('site_timezone_offset_minutes', {
      type: 'integer',
      nullable: true,
      raw_default: null,
      effective_fallback: null,
      step: 30,
    });
    const server = installSiteConfigServer(
      {
        revision: '1',
        values: { anthropic_default_max_tokens: null, site_timezone_offset_minutes: null },
      },
      [anthropic, timezone],
    );
    const rendered = await renderSettings();
    await rendered.user.click((await screen.findByText('Other (fixture)')).closest('button')!);

    let anthropicInput = screen.getByLabelText('Default Anthropic max output tokens');
    let anthropicForm = anthropicInput.closest<HTMLElement>('.ops-setting');
    expect(anthropicForm).not.toBeNull();
    expect(anthropicInput).toBeDisabled();
    await rendered.user.click(within(anthropicForm!).getByLabelText('Remove override'));
    expect(anthropicInput).toBeEnabled();
    for (const invalid of ['1e3', '0', '1.5', '01']) {
      fireEvent.change(anthropicInput, { target: { value: invalid } });
      expect(within(anthropicForm!).getByRole('alert')).toHaveTextContent(
        /canonical numeric setting value/i,
      );
      expect(screen.getByRole('button', { name: 'Save all changes' })).toBeDisabled();
    }
    expect(server.patches).toHaveLength(0);

    fireEvent.change(anthropicInput, { target: { value: '131072' } });
    await rendered.user.click(screen.getByRole('button', { name: 'Save all changes' }));
    await waitFor(() =>
      expect(server.patches).toContainEqual({
        path: '/admin/api/site-config/anthropic_default_max_tokens',
        value: 131_072,
      }),
    );
    anthropicInput = await screen.findByLabelText('Default Anthropic max output tokens');
    anthropicForm = anthropicInput.closest<HTMLElement>('.ops-setting');
    await rendered.user.click(within(anthropicForm!).getByLabelText('Remove override'));
    await rendered.user.click(screen.getByRole('button', { name: 'Save all changes' }));
    await waitFor(() => expect(server.patches.at(-1)?.value).toBeNull());

    let timezoneInput = await screen.findByLabelText('Site timezone offset');
    const timezoneForm = timezoneInput.closest<HTMLElement>('.ops-setting');
    expect(timezoneForm).not.toBeNull();
    expect(timezoneInput).toBeEnabled();
    expect(timezoneInput).toHaveAttribute('min', '-720');
    expect(timezoneInput).toHaveAttribute('max', '840');
    expect(timezoneInput).toHaveAttribute('step', '30');
    const patchCount = server.patches.length;
    fireEvent.change(timezoneInput, { target: { value: '345' } });
    expect(within(timezoneForm!).getByRole('alert')).toHaveTextContent(
      /canonical numeric setting value/i,
    );
    expect(screen.getByRole('button', { name: 'Save all changes' })).toBeDisabled();
    expect(server.patches).toHaveLength(patchCount);

    fireEvent.change(timezoneInput, { target: { value: '-300' } });
    expect(within(timezoneForm!).getByText('Preview: UTC-05:00')).toBeVisible();
    await rendered.user.click(screen.getByRole('button', { name: 'Save all changes' }));
    await waitFor(() => expect(server.patches.at(-1)?.value).toBe(-300));
    timezoneInput = await screen.findByLabelText('Site timezone offset');
    expect(timezoneInput).toHaveValue(-300);
  });

  test('accepts only the closed Generation 2 catalog entry shape', () => {
    const current = catalogEntry('site_name', {
      type: 'string',
      title: { zh: '站点名称', en: 'Site name' },
      unit: null,
      raw_default: 'NonbiriAPI',
      effective_fallback: 'NonbiriAPI',
      minimum: 1,
      maximum: 256,
      step: null,
    });
    expect(normalizeSiteConfigCatalogEntry(current)).toEqual(current);
    expect(() =>
      normalizeSiteConfigCatalogEntry({
        ...current,
        type: undefined,
        value_type: 'text',
      }),
    ).toThrow(/site configuration catalog entry/i);
    expect(() => normalizeSiteConfigCatalogEntry({ ...current, alpha_optional: true })).toThrow(
      /site configuration catalog entry/i,
    );
  });

  test('rejects the stale alpha flat settings snapshot', async () => {
    const siteName = catalogEntry('site_name', {
      type: 'string',
      title: { zh: '站点名称', en: 'Site name' },
      unit: null,
      raw_default: 'NonbiriAPI',
      effective_fallback: 'NonbiriAPI',
      minimum: 1,
      maximum: 256,
      step: null,
    });
    vi.stubGlobal(
      'fetch',
      vi.fn(async (input: string | URL | Request) => {
        const path = new URL(
          input instanceof Request ? input.url : String(input),
          window.location.origin,
        ).pathname;
        if (path === '/admin/api/site-config/catalog') return jsonResponse({ data: [siteName] });
        if (path === '/admin/api/site-config') return jsonResponse({ site_name: 'Alpha fixture' });
        if (path === '/admin/api/maintenance') {
          return jsonResponse({ enabled: false, revision: '1' });
        }
        if (path === '/admin/api/legal-holds') return jsonResponse({ data: [], next_cursor: null });
        throw new Error(`Unexpected fixture request: GET ${path}`);
      }),
    );
    await renderSettings();
    expect(await screen.findByText(/service returned an invalid response/i)).toBeVisible();
    expect(screen.queryByLabelText('Site name')).not.toBeInTheDocument();
  });

  test('preserves line endings at the exact 65536-byte legal boundary and rejects one byte more', async () => {
    const prefix = '  标题\r\n\tparagraph \r\n';
    const prefixBytes = new TextEncoder().encode(prefix).byteLength;
    const document = prefix + 'x'.repeat(65_536 - prefixBytes);
    expect(new TextEncoder().encode(document)).toHaveLength(65_536);
    const legal = catalogEntry('legal_terms_override_en', {
      group: 'legal',
      type: 'text',
      title: { zh: '服务条款覆盖（英文）', en: 'Terms override (English)' },
      description: { zh: '逐字节保留', en: 'Preserved byte for byte' },
      unit: null,
      raw_default: '',
      effective_fallback: '',
      minimum: 0,
      maximum: 65_536,
      step: null,
    });
    const server = installSiteConfigServer(
      { revision: '9', values: { legal_terms_override_en: document } },
      [legal],
    );
    const rendered = await renderSettings();
    await rendered.user.click((await screen.findByText('Legal text')).closest('button')!);
    let textarea = screen.getByLabelText('Terms override (English)');
    let form = textarea.closest<HTMLElement>('.ops-setting');
    expect(form).not.toBeNull();
    expect(within(form!).getByText('65536 / 65536 UTF-8 bytes')).toBeVisible();
    let save = screen.getByRole('button', { name: 'Save all changes' });
    expect(save).toBeDisabled();
    expect(server.patches).toHaveLength(0);

    const browserDocument = document.replaceAll('\r\n', '\n');
    const editedBrowserDocument = `${browserDocument.slice(0, -1)}y`;
    const editedDocument = `${document.slice(0, -1)}y`;
    textarea = await screen.findByLabelText('Terms override (English)');
    save = screen.getByRole('button', { name: 'Save all changes' });
    fireEvent.change(textarea, { target: { value: editedBrowserDocument } });
    await rendered.user.click(save);
    await waitFor(() => expect(server.patches).toHaveLength(1));
    expect(server.patches[0]?.value === editedDocument).toBe(true);
    expect(String(server.patches[0]?.value).replaceAll('\r\n', '')).not.toContain('\n');

    textarea = await screen.findByLabelText('Terms override (English)');
    form = textarea.closest<HTMLElement>('.ops-setting');
    fireEvent.change(textarea, { target: { value: 'x'.repeat(65_537) } });
    expect(within(form!).getByText('65537 / 65536 UTF-8 bytes')).toBeVisible();
    expect(within(form!).getByRole('alert')).toHaveTextContent(/no larger than 65536 UTF-8 bytes/i);
    expect(screen.getByRole('button', { name: 'Save all changes' })).toBeDisabled();
    expect(server.patches).toHaveLength(1);
  });

  test('renders exact amount and human-readable seconds previews without float conversion', async () => {
    const amount = catalogEntry('credits_cap', {
      type: 'amount',
      title: { zh: '签到积分门槛', en: 'Check-in credit threshold' },
      unit: { zh: '积分', en: 'credits' },
      raw_default: '0',
      effective_fallback: '0',
      minimum: '0',
      maximum: '9000000000000',
      step: '0.001',
    });
    const seconds = catalogEntry('rpm_ban_duration_seconds', {
      title: { zh: 'RPM 封禁时长', en: 'RPM auto-ban duration' },
      unit: { zh: '秒', en: 'seconds' },
      raw_default: 3_661,
      effective_fallback: 3_661,
      minimum: 1,
      maximum: 86_400,
      step: 1,
    });
    const server = installSiteConfigServer(
      {
        revision: '11',
        values: { credits_cap: '1234567.089', rpm_ban_duration_seconds: 3_661 },
      },
      [amount, seconds],
    );
    const rendered = await renderSettings();
    await rendered.user.click((await screen.findByText('Other (fixture)')).closest('button')!);
    expect(screen.getByText('1,234,567.089 credits')).toBeVisible();
    expect(screen.getByText('Human-readable duration: 1h 1m 1s')).toBeVisible();

    const amountInput = screen.getByLabelText('Check-in credit threshold');
    expect(amountInput).toHaveAttribute('min', '0');
    expect(amountInput).toHaveAttribute('max', '9000000000000');
    expect(amountInput).toHaveAttribute('step', '0.001');
    const amountForm = amountInput.closest<HTMLElement>('.ops-setting');
    fireEvent.change(amountInput, { target: { value: '1.230' } });
    expect(screen.getByRole('button', { name: 'Save all changes' })).toBeEnabled();
    await rendered.user.click(screen.getByRole('button', { name: 'Save all changes' }));
    await waitFor(() => expect(server.patches.at(-1)?.value).toBe('1.23'));

    fireEvent.change(amountInput, { target: { value: '9000000000000' } });
    expect(within(amountForm!).getByText('9,000,000,000,000 credits')).toBeVisible();
    await rendered.user.click(screen.getByRole('button', { name: 'Save all changes' }));
    await waitFor(() => expect(server.patches.at(-1)?.value).toBe('9000000000000'));
  });

  test('does not claim a timezone conflict restored authority when the refetch fails', async () => {
    const timezone = catalogEntry('site_timezone_offset_minutes', {
      type: 'integer',
      nullable: true,
      raw_default: null,
      effective_fallback: null,
      step: 30,
    });
    let configReads = 0;
    let patchWrites = 0;
    vi.stubGlobal(
      'fetch',
      vi.fn(async (input: string | URL | Request, init?: RequestInit) => {
        const path = new URL(
          input instanceof Request ? input.url : String(input),
          window.location.origin,
        ).pathname;
        const method = (
          init?.method ?? (input instanceof Request ? input.method : 'GET')
        ).toUpperCase();
        if (method === 'GET' && path === '/admin/api/site-config/catalog') {
          return jsonResponse({ data: [timezone] });
        }
        if (method === 'GET' && path === '/admin/api/site-config') {
          configReads += 1;
          return configReads === 1
            ? jsonResponse({
                revision: '4',
                values: { site_timezone_offset_minutes: 0 },
              })
            : jsonResponse(
                { error: { code: 'service_unavailable', message: 'refresh failed' } },
                503,
              );
        }
        if (method === 'GET' && path === '/admin/api/maintenance') {
          return jsonResponse({ enabled: false, revision: '1' });
        }
        if (method === 'GET' && path === '/admin/api/legal-holds') {
          return jsonResponse({ data: [], next_cursor: null });
        }
        if (method === 'PATCH' && path === '/admin/api/site-config') {
          patchWrites += 1;
          return jsonResponse({ error: { code: 'conflict', message: 'timezone locked' } }, 409);
        }
        throw new Error(`Unexpected fixture request: ${method} ${path}`);
      }),
    );
    const rendered = await renderSettings();
    await rendered.user.click((await screen.findByText('Other (fixture)')).closest('button')!);
    const input = screen.getByLabelText('Site timezone offset');
    fireEvent.change(input, { target: { value: '30' } });
    await rendered.user.click(screen.getByRole('button', { name: 'Save all changes' }));
    await waitFor(() => expect(configReads).toBe(2));
    expect(patchWrites).toBe(1);
    expect(input).toHaveValue(30);
    expect(screen.getByText('timezone locked')).toBeVisible();
    expect(screen.getByText('refresh failed')).toBeVisible();
    expect(screen.queryByText(/has been restored/i)).not.toBeInTheDocument();
  });

  test('disables a setting editor and authority actions while save is pending', async () => {
    const siteName = catalogEntry('site_name', {
      type: 'string',
      title: { zh: '站点名称', en: 'Site name' },
      unit: null,
      raw_default: 'Before',
      effective_fallback: 'Before',
      minimum: 1,
      maximum: 256,
      step: null,
    });
    const snapshot: SiteConfigSnapshot = {
      revision: '1',
      values: { site_name: 'Before' },
    };
    let patchBody: unknown;
    let resolvePatch: ((response: Response) => void) | undefined;
    const pendingPatch = new Promise<Response>((resolve) => {
      resolvePatch = resolve;
    });
    vi.stubGlobal(
      'fetch',
      vi.fn(async (input: string | URL | Request, init?: RequestInit) => {
        const path = new URL(
          input instanceof Request ? input.url : String(input),
          window.location.origin,
        ).pathname;
        const method = (
          init?.method ?? (input instanceof Request ? input.method : 'GET')
        ).toUpperCase();
        if (method === 'GET' && path === '/admin/api/site-config/catalog') {
          return jsonResponse({ data: [siteName] });
        }
        if (method === 'GET' && path === '/admin/api/site-config') return jsonResponse(snapshot);
        if (method === 'GET' && path === '/admin/api/maintenance') {
          return jsonResponse({ enabled: false, revision: '1' });
        }
        if (method === 'GET' && path === '/admin/api/legal-holds') {
          return jsonResponse({ data: [], next_cursor: null });
        }
        if (method === 'PATCH' && path === '/admin/api/site-config') {
          patchBody = JSON.parse(String(init?.body));
          return pendingPatch;
        }
        throw new Error(`Unexpected fixture request: ${method} ${path}`);
      }),
    );
    const rendered = await renderSettings();
    await rendered.user.click((await screen.findByText('Other (fixture)')).closest('button')!);
    const input = screen.getByLabelText('Site name');
    const form = input.closest<HTMLElement>('.ops-setting');
    fireEvent.change(input, { target: { value: 'After' } });
    await rendered.user.click(screen.getByRole('button', { name: 'Save all changes' }));

    expect(patchBody).toEqual({ expected_revision: '1', values: { site_name: 'After' } });
    expect(input).toBeDisabled();
    expect(screen.getByRole('button', { name: 'Working…' })).toBeDisabled();
    expect(within(form!).getByRole('button', { name: 'Undo this change' })).toBeDisabled();
    expect(screen.getByRole('button', { name: 'Discard changes' })).toBeDisabled();

    snapshot.revision = '2';
    snapshot.values.site_name = 'After';
    resolvePatch?.(jsonResponse({ changed_keys: ['site_name'], revision: '2' }));
    await waitFor(() => expect(screen.getByLabelText('Site name')).toBeEnabled());
    expect(screen.getByLabelText('Site name')).toHaveValue('After');
  });
});

describe('admin per-user limit explanations', () => {
  test('states the built-in concurrency default and independent gate composition', async () => {
    const adminUser = {
      id: '7',
      username: 'fixture-user',
      discord_id: '1234567890123456789',
      avatar_url: null,
      guild_nick: null,
      guild_avatar_url: null,
      is_admin: false,
      is_banned: false,
      banned_reason: '',
      banned_until: null,
      charity_suspended_until: null,
      endpoint_limit: null,
      effective_endpoint_limit: '4',
      rpm_limit: null,
      effective_rpm_limit: '60',
      concurrency_limit: null,
      effective_concurrency_limit: '5',
      lang: 'en',
      balance: '0',
      donation_credit: '0',
      level: { manual: null, automatic: 1, effective: 1, display_name: 'Lv1' },
      game_profile_public: false,
      revision: '1',
      usage: {
        total_requests: '0',
        total_uncached_input_tokens: '0',
        total_cache_write_input_tokens: '0',
        total_cache_read_input_tokens: '0',
        total_output_tokens: '0',
        total_prompt_tokens: '0',
        total_completion_tokens: '0',
        total_unknown_usage_requests: '0',
      },
      created_at: 1_700_000_000,
      updated_at: 1_700_000_001,
    };
    installJsonFetchFixtures([
      {
        method: 'GET',
        path: '/admin/api/users?limit=50',
        body: { data: [adminUser], next_cursor: null },
      },
      { method: 'GET', path: '/admin/api/users/7', body: adminUser },
    ]);
    const rendered = await renderWithProviders(<UsersPage />, {
      station: 'admin',
      locale: 'en',
      role: 'admin',
    });
    await screen.findByRole('columnheader', { name: 'User ID' });
    expect(screen.getByRole('columnheader', { name: 'Discord ID' })).toBeVisible();
    const row = (await screen.findByText('fixture-user')).closest('tr');
    expect(row).not.toBeNull();
    const cells = within(row!).getAllByRole('cell');
    expect(cells[0]).toHaveTextContent(/^7$/);
    expect(cells[1]).toHaveTextContent(/^fixture-user$/);
    await rendered.user.click(within(row!).getByRole('button', { name: 'Copy Discord ID' }));
    expect(await navigator.clipboard.readText()).toBe('1234567890123456789');
    await rendered.user.click(await screen.findByRole('button', { name: 'Manage' }));
    const note = screen.getByText(/built-in default of 5/i);
    expect(note).toHaveTextContent(/global RPM, global egress, endpoint and key gates/i);
    expect(note).toHaveTextContent(/effective is not the final minimum/i);
    await rendered.i18n.changeLanguage('zh');
    expect(await screen.findByText(/内建默认值 5/)).toHaveTextContent(
      /全站 RPM、全站出站并发、端点和密钥/,
    );
    expect(screen.getByText(/内建默认值 5/)).toHaveTextContent(/不是所有门禁取最小后的最终上限/);
  });
});

const initialGameConfig: GamesConfig = {
  revision: '7',
  master_enabled: true,
  fishing: {
    enabled: false,
    bait_prices: { worm: '2.5', lure: '5', premium: '7.5' },
    rtp_percent: { standard: 90, premium: 88 },
    treasure_multipliers: { bottle: 2, clover: 3, shell: 5 },
  },
  linklink: {
    enabled: true,
    specs: {
      '6x8': { enabled: true, price: '1' },
      '8x8': { enabled: true, price: '2' },
      '10x10': { enabled: false, price: '3.125' },
    },
  },
  rps: {
    enabled: true,
    modes: {
      quick: {
        enabled: true,
        base: '1',
        pumps_bp: { platform: 100, welfare: 200, thursday: 300 },
        queue_seconds: 60,
        gesture_seconds: 10,
        dealer_seconds: 10,
        follower_seconds: 10,
        queue_capacity: 1_024,
      },
      standard: {
        enabled: true,
        base: '2',
        pumps_bp: { platform: 200, welfare: 300, thursday: 400 },
        queue_seconds: 90,
        gesture_seconds: 15,
        dealer_seconds: 12,
        follower_seconds: 12,
        queue_capacity: 2_048,
      },
      deathmatch: {
        enabled: false,
        base: '3',
        pumps_bp: { platform: 300, welfare: 400, thursday: 500 },
        queue_seconds: 120,
        gesture_seconds: 20,
        dealer_seconds: 15,
        follower_seconds: 15,
        queue_capacity: 4_096,
      },
    },
  },
};

const activeGameCounts: ActiveCounts = {
  games: [
    { game: 'fishing', mode: null, spec: null, phase: 'casting', count: '9007199254740993' },
    { game: 'linklink', mode: null, spec: '10x10', phase: 'playing', count: '2' },
    { game: 'rps', mode: 'deathmatch', spec: null, phase: 'gesture', count: '3' },
  ],
  queues: [
    { mode: 'quick', count: '5' },
    { mode: 'standard', count: '0' },
    { mode: 'deathmatch', count: '7' },
  ],
};

function cloneGameConfig(value: GamesConfig): GamesConfig {
  return structuredClone(value);
}

function installGameServer(options: { rejectPatch?: boolean } = {}) {
  let state = cloneGameConfig(initialGameConfig);
  const patches: Record<string, unknown>[] = [];
  const fetchMock = vi.fn(async (input: string | URL | Request, init?: RequestInit) => {
    const path = new URL(
      input instanceof Request ? input.url : String(input),
      window.location.origin,
    ).pathname;
    const method = (
      init?.method ?? (input instanceof Request ? input.method : 'GET')
    ).toUpperCase();
    if (method === 'GET' && path === '/admin/api/games/config') return jsonResponse(state);
    if (method === 'GET' && path === '/admin/api/games/active-counts') {
      return jsonResponse(activeGameCounts);
    }
    if (path !== '/admin/api/games/config') throw new Error(`Unexpected path ${path}`);
    if (method !== 'PATCH') throw new Error(`Unexpected method ${method}`);
    const patch = JSON.parse(String(init?.body)) as Record<string, unknown>;
    patches.push(patch);
    if (options.rejectPatch) {
      return jsonResponse(
        { error: { code: 'invalid_request', message: 'Invalid fishing economy.' } },
        400,
      );
    }
    const mutable = patch as {
      expected_revision: string;
      master_enabled: boolean;
      fishing: GamesConfig['fishing'];
      linklink: GamesConfig['linklink'];
      rps: {
        enabled: boolean;
        modes: Record<
          'quick' | 'standard' | 'deathmatch',
          Omit<GamesConfig['rps']['modes']['quick'], 'queue_capacity'>
        >;
      };
    };
    const previous = state;
    state = {
      revision: String(BigInt(state.revision) + 1n),
      master_enabled: mutable.master_enabled,
      fishing: structuredClone(mutable.fishing),
      linklink: structuredClone(mutable.linklink),
      rps: {
        enabled: mutable.rps.enabled,
        modes: {
          quick: {
            ...structuredClone(mutable.rps.modes.quick),
            queue_capacity: previous.rps.modes.quick.queue_capacity,
          },
          standard: {
            ...structuredClone(mutable.rps.modes.standard),
            queue_capacity: previous.rps.modes.standard.queue_capacity,
          },
          deathmatch: {
            ...structuredClone(mutable.rps.modes.deathmatch),
            queue_capacity: previous.rps.modes.deathmatch.queue_capacity,
          },
        },
      },
    };
    return jsonResponse(state);
  });
  vi.stubGlobal('fetch', fetchMock);
  return {
    fetchMock,
    patches,
    get state() {
      return state;
    },
  };
}

describe('standalone Admin Games feature', () => {
  test('sends the frozen full mutable PATCH, excludes queue capacity, and renders exact active counts', async () => {
    const server = installGameServer();
    const rendered = await renderWithProviders(<GamesPage />, {
      station: 'admin',
      locale: 'en',
      role: 'admin',
    });
    const save = await screen.findByRole('button', { name: 'Save game configuration' });
    expect(screen.getByText(/Phase: Unknown value \(casting\) · 9007199254740993/)).toBeVisible();
    expect(
      screen.getByText(/Specification: 10×10 · Phase: Unknown value \(playing\) · 2/),
    ).toBeVisible();
    expect(screen.getByText(/Mode: Deathmatch · Phase: Gesture selection · 3/)).toBeVisible();
    const queues = screen.getByRole('heading', { name: 'RPS queues' }).closest('section');
    expect(queues).toHaveTextContent('Quick: 5');
    expect(queues).toHaveTextContent('Standard: 0');
    expect(queues).toHaveTextContent('Deathmatch: 7');

    const worm = screen.getByLabelText(/worm bait price/i);
    fireEvent.change(worm, { target: { value: '3' } });
    await rendered.user.click(save);
    await waitFor(() => expect(server.patches).toHaveLength(1));
    expect(server.patches[0]).toEqual({
      expected_revision: '7',
      master_enabled: true,
      fishing: {
        ...initialGameConfig.fishing,
        bait_prices: { ...initialGameConfig.fishing.bait_prices, worm: '3' },
      },
      linklink: initialGameConfig.linklink,
      rps: {
        enabled: true,
        modes: {
          quick: {
            enabled: true,
            base: '1',
            pumps_bp: { platform: 100, welfare: 200, thursday: 300 },
            queue_seconds: 60,
            gesture_seconds: 10,
            dealer_seconds: 10,
            follower_seconds: 10,
          },
          standard: {
            enabled: true,
            base: '2',
            pumps_bp: { platform: 200, welfare: 300, thursday: 400 },
            queue_seconds: 90,
            gesture_seconds: 15,
            dealer_seconds: 12,
            follower_seconds: 12,
          },
          deathmatch: {
            enabled: false,
            base: '3',
            pumps_bp: { platform: 300, welfare: 400, thursday: 500 },
            queue_seconds: 120,
            gesture_seconds: 20,
            dealer_seconds: 15,
            follower_seconds: 15,
          },
        },
      },
    });
    expect(JSON.stringify(server.patches[0])).not.toContain('queue_capacity');
    await waitFor(() => expect(save).toBeDisabled());
    expect(screen.getByLabelText(/worm bait price/i)).toHaveValue(3);
    expect(screen.getByText('1024')).toBeVisible();
  });

  test('blocks local range violations but leaves full economy compilation authoritative', async () => {
    const server = installGameServer({ rejectPatch: true });
    const rendered = await renderWithProviders(<GamesPage />, {
      station: 'admin',
      locale: 'en',
      role: 'admin',
    });
    const save = await screen.findByRole('button', { name: 'Save game configuration' });
    const quickPlatform = screen.getByLabelText(/quick platform cut/i);
    fireEvent.change(quickPlatform, { target: { value: '9999' } });
    await rendered.user.click(save);
    expect(screen.getByRole('alert')).toHaveTextContent(/must total less than 10000 basis points/i);
    expect(server.patches).toHaveLength(0);

    fireEvent.change(quickPlatform, { target: { value: '100' } });
    const input = screen.getByLabelText('Standard bait RTP');
    fireEvent.change(input, { target: { value: '100' } });
    await rendered.user.click(save);
    expect(await screen.findByText('Invalid fishing economy.')).toBeVisible();
    expect(input).toHaveValue(100);
    expect(server.state).toEqual(initialGameConfig);
    expect(server.patches).toHaveLength(1);
    fireEvent.change(screen.getByLabelText('Lucky clover'), {
      target: { value: '4' },
    });
    expect(screen.queryByText('Invalid fishing economy.')).not.toBeInTheDocument();
  });

  test('disables every editor while a late authoritative response is pending', async () => {
    let state = cloneGameConfig(initialGameConfig);
    let resolvePatch: ((response: Response) => void) | undefined;
    const pendingPatch = new Promise<Response>((resolve) => {
      resolvePatch = resolve;
    });
    vi.stubGlobal(
      'fetch',
      vi.fn(async (input: string | URL | Request, init?: RequestInit) => {
        const path = new URL(
          input instanceof Request ? input.url : String(input),
          window.location.origin,
        ).pathname;
        const method = (
          init?.method ?? (input instanceof Request ? input.method : 'GET')
        ).toUpperCase();
        if (method === 'GET' && path === '/admin/api/games/config') return jsonResponse(state);
        if (method === 'GET' && path === '/admin/api/games/active-counts') {
          return jsonResponse(activeGameCounts);
        }
        if (method === 'PATCH' && path === '/admin/api/games/config') return pendingPatch;
        throw new Error(`Unexpected fixture request: ${method} ${path}`);
      }),
    );
    const rendered = await renderWithProviders(<GamesPage />, {
      station: 'admin',
      locale: 'en',
      role: 'admin',
    });
    const worm = await screen.findByLabelText(/worm bait price/i);
    fireEvent.change(worm, { target: { value: '3' } });
    await rendered.user.click(screen.getByRole('button', { name: 'Save game configuration' }));
    expect(worm).toBeDisabled();
    expect(screen.getByLabelText('Games master switch')).toBeDisabled();
    expect(screen.getByLabelText(/6×8 entry price/i)).toBeDisabled();
    expect(screen.getByLabelText(/quick base/i)).toBeDisabled();
    expect(screen.getByRole('button', { name: 'Working…' })).toBeDisabled();
    expect(screen.getByRole('button', { name: 'Restore authority values' })).toBeDisabled();

    state = {
      ...state,
      revision: '8',
      fishing: {
        ...state.fishing,
        bait_prices: { ...state.fishing.bait_prices, worm: '3' },
      },
    };
    resolvePatch?.(jsonResponse(state));
    await waitFor(() => expect(screen.getByLabelText(/worm bait price/i)).toBeEnabled());
    expect(screen.getByLabelText(/worm bait price/i)).toHaveValue(3);
  });

  test('strictly rejects stale or malformed config and active-count DTOs', () => {
    expect(normalizeGamesConfig(initialGameConfig)).toEqual(initialGameConfig);
    expect(() =>
      normalizeGamesConfig({
        master_enabled: initialGameConfig.master_enabled,
        fishing: initialGameConfig.fishing,
      }),
    ).toThrow(/games configuration/i);
    expect(() => normalizeGamesConfig({ ...initialGameConfig, master_enabled: 0 })).toThrow(
      /master switch/i,
    );
    expect(() =>
      normalizeGamesConfig({
        ...initialGameConfig,
        fishing: {
          ...initialGameConfig.fishing,
          bait_prices: { ...initialGameConfig.fishing.bait_prices, worm: '01' },
        },
      }),
    ).toThrow(/worm price/i);
    expect(() =>
      normalizeGamesConfig({
        ...initialGameConfig,
        rps: {
          ...initialGameConfig.rps,
          modes: {
            ...initialGameConfig.rps.modes,
            quick: {
              enabled: true,
              base: '1',
              pumps_bp: { platform: 100, welfare: 200, thursday: 300 },
              queue_seconds: 60,
              gesture_seconds: 10,
              dealer_seconds: 10,
              follower_seconds: 10,
            },
          },
        },
      }),
    ).toThrow(/quick RPS/i);

    expect(normalizeActiveCounts(activeGameCounts)).toEqual(activeGameCounts);
    expect(() => normalizeActiveCounts({ active: [] })).toThrow(/active game counts/i);
    expect(() =>
      normalizeActiveCounts({
        ...activeGameCounts,
        games: [{ ...activeGameCounts.games[0], count: 1 }],
      }),
    ).toThrow(/active game count/i);
  });
});

function completePrices() {
  return {
    request_user_price_milli: '0',
    request_donor_reward_milli: '0',
    uncached_user_price_milli: '0',
    cache_write_user_price_milli: '0',
    cache_read_user_price_milli: '0',
    output_user_price_milli: '0',
    uncached_donor_reward_milli: '0',
    cache_write_donor_reward_milli: '0',
    cache_read_donor_reward_milli: '0',
    output_donor_reward_milli: '0',
    current_request_user_price_milli: '0',
    current_uncached_user_price_milli: '0',
    current_cache_write_user_price_milli: '0',
    current_cache_read_user_price_milli: '0',
    current_output_user_price_milli: '0',
  };
}

function endpointKeyFixture(
  id: unknown,
  forceStoreFalse: unknown = false,
  connectorType = 'openai-compatible',
) {
  const fixture = {
    id,
    note: '',
    enabled: true,
    force_store_false: forceStoreFalse,
    created_at: 1,
    updated_at: 2,
  };
  if (connectorType === 'anthropic-compatible')
    delete (fixture as Record<string, unknown>).force_store_false;
  return fixture;
}

function platformModelFixture(id: unknown, overrides: Record<string, unknown> = {}) {
  return {
    id,
    provider: 'fixture-provider',
    model: 'fixture-model',
    full_name: 'Fixture model',
    route_strategy: 'ordered',
    silent_retry: false,
    flatten_tool_calls: false,
    binding_count: 0,
    created_at: 1,
    updated_at: 2,
    ...overrides,
  };
}

function charityModelFixture(id: unknown, overrides: Record<string, unknown> = {}) {
  return {
    id,
    provider: 'fixture-provider',
    model: 'fixture-model',
    full_name: 'Fixture model',
    enabled: true,
    flatten_tool_calls: false,
    pricing_mode: 'per_request',
    prices: completePrices(),
    discount: { percent: 100, enabled: false },
    success_samples: 0,
    success_count: 0,
    available: true,
    availability_reason: 'ok',
    ...overrides,
  };
}

describe('B1 and U3-U5 additive wire normalizers', () => {
  test('keeps nullable raw limits distinct from required effective limits', () => {
    const normalizedUser = normalizeUserSummary(coreBaseUser);
    expect(normalizedUser.endpoint_limit).toBeNull();
    expect(normalizedUser.rpm_limit).toBeNull();
    expect(normalizedUser.concurrency_limit).toBeNull();
    expect(normalizedUser).toMatchObject({
      effective_endpoint_limit: '4',
      effective_rpm_limit: '60',
      effective_concurrency_limit: '5',
    });
    const admin = normalizeAdminUser({
      id: 7,
      username: 'fixture-user',
      discord_id: 'discord',
      is_banned: false,
      endpoint_limit: 50,
      effective_endpoint_limit: 50,
      rpm_limit: 4000,
      effective_rpm_limit: 4000,
      concurrency_limit: 70000,
      effective_concurrency_limit: 70000,
      total_requests: 0,
      total_prompt_tokens: 0,
      total_completion_tokens: 0,
      total_unknown_usage_requests: 0,
      credits_balance: '0',
      donation_credit_balance: '0',
      auto_level: 1,
      created_at: '2026-08-23T00:00:00Z',
    });
    expect(admin).toMatchObject({ endpoint_limit: 50, rpm_limit: 4000, concurrency_limit: 70000 });
    expect(() => normalizeAdminUser({ ...admin, effective_concurrency_limit: 100_001 })).toThrow(
      /effective concurrency limit/i,
    );
  });

  test('projects policy ownership fields and preserves strict donation limits and amounts', () => {
    expect(
      normalizeEndpointKey(endpointKeyFixture(1, true), 'openai-compatible').force_store_false,
    ).toBe(true);
    expect(
      normalizePlatformModel(platformModelFixture(2, { flatten_tool_calls: true }))
        .flatten_tool_calls,
    ).toBe(true);
    const modelFixture = charityModelFixture(3, { flatten_tool_calls: true });
    expect(normalizeCharityModel(modelFixture).flatten_tool_calls).toBe(true);
    expect(normalizeManagementCharityModel(modelFixture).flatten_tool_calls).toBe(true);
    const keyFixture = {
      id: 4,
      max_concurrency: 100_000,
      rpm_limit: 4_096,
      credits_usage_cap_milli: '9007199254740993',
      credits_used_milli: '0',
      credits_reserved_milli: '0',
      enabled: true,
      force_store_false: true,
    };
    expect(normalizeDonationKey(keyFixture, 'openai-compatible')).toMatchObject({
      max_concurrency: 100_000,
      rpm_limit: 4_096,
      credits_usage_cap_milli: '9007199254740993',
      force_store_false: true,
    });
    expect(normalizeManagementDonationKey(keyFixture).force_store_false).toBe(true);
    expect(() => normalizeEndpointKey(endpointKeyFixture(1, 1), 'openai-compatible')).toThrow(
      /store policy/i,
    );
    expect(() =>
      normalizePlatformModel(platformModelFixture(2, { flatten_tool_calls: 'true' })),
    ).toThrow(/tool-call policy/i);
    expect(() =>
      normalizeDonationKey({ ...keyFixture, rpm_limit: 4_097 }, 'openai-compatible'),
    ).toThrow(/donation key RPM/i);
    expect(() =>
      normalizeManagementDonationKey({
        ...keyFixture,
        credits_usage_cap_milli: '01',
      }),
    ).toThrow(/amount/i);
  });

  test('preserves Anthropic policy absence while rejecting malformed policy and connector fields', () => {
    const keyFixture = {
      id: 4,
      max_concurrency: 1,
      rpm_limit: 2,
      credits_usage_cap_milli: '0',
      credits_used_milli: '0',
      credits_reserved_milli: '0',
      enabled: true,
    };
    expect(normalizeDonationKey(keyFixture, 'anthropic-compatible').force_store_false).toBe(
      'not_applicable',
    );
    expect(normalizeManagementDonationKey(keyFixture).force_store_false).toBe('not_applicable');
    expect(
      normalizeDonationKey({ ...keyFixture, force_store_false: false }, 'openai-compatible')
        .force_store_false,
    ).toBe(false);
    expect(
      normalizeDonationKey({ ...keyFixture, force_store_false: true }, 'openai-compatible')
        .force_store_false,
    ).toBe(true);
    expect(() =>
      normalizeDonationKey({ ...keyFixture, force_store_false: null }, 'openai-compatible'),
    ).toThrow(/store policy/i);
    expect(() =>
      normalizeDonationKey({ ...keyFixture, force_store_false: 'false' }, 'openai-compatible'),
    ).toThrow(/store policy/i);
    expect(() =>
      normalizePlatformModel(platformModelFixture(2, { route_strategy: undefined })),
    ).toThrow(/route strategy/i);
    expect(() =>
      normalizePlatformModel(platformModelFixture(2, { silent_retry: undefined })),
    ).toThrow(/silent-retry policy/i);
    expect(() =>
      normalizePlatformModel(platformModelFixture(2, { flatten_tool_calls: undefined })),
    ).toThrow(/tool-call policy/i);
    expect(() =>
      normalizePlatformModel(platformModelFixture(2, { route_strategy: 'invalid' })),
    ).toThrow(/route strategy/i);
    expect(() =>
      normalizeCharityModel(charityModelFixture(3, { flatten_tool_calls: undefined })),
    ).toThrow(/tool-call policy/i);
    expect(() =>
      normalizeManagementCharityModel(charityModelFixture(3, { flatten_tool_calls: undefined })),
    ).toThrow(/tool-call policy/i);
    expect(() => normalizeCoreEndpoint({ id: '1' })).toThrow(/invalid endpoint/i);
    expect(() =>
      normalizeCoreEndpoint({
        id: '1',
        connector_type: 'unknown',
        base_url: 'https://upstream.test/v1',
        origin: { kind: 'custom' },
        note: '',
        enabled: true,
        revision: '1',
        key_count: '0',
        created_at: 1,
        updated_at: 2,
      }),
    ).toThrow(/connector type/i);
    expect(() =>
      normalizeEndpointKey({ ...endpointKeyFixture(1), id: undefined }, 'openai-compatible'),
    ).toThrow(/endpoint key id/i);
  });

  test('requires connector-specific policy fields and rejects non-Unix or incomplete projections', () => {
    expect(() =>
      normalizeEndpointKey(
        { ...endpointKeyFixture(1), force_store_false: undefined },
        'openai-compatible',
      ),
    ).toThrow(/store policy/i);
    expect(
      normalizeEndpointKey(
        endpointKeyFixture(1, false, 'anthropic-compatible'),
        'anthropic-compatible',
      ).force_store_false,
    ).toBe('not_applicable');
    expect(() =>
      normalizeEndpointKey(
        { ...endpointKeyFixture(1, false, 'anthropic-compatible'), force_store_false: false },
        'anthropic-compatible',
      ),
    ).toThrow(/unexpected store policy/i);

    const validEndpoint = {
      id: '1',
      connector_type: 'openai-compatible',
      base_url: 'https://upstream.test/v1',
      origin: { kind: 'custom' },
      note: '',
      enabled: true,
      revision: '1',
      key_count: '0',
      created_at: 1,
      updated_at: 2,
    };
    expect(() =>
      normalizeCoreEndpoint({
        ...validEndpoint,
        created_at: '2026-08-23T00:00:00Z',
      }),
    ).toThrow(/creation time/i);
    expect(() => normalizeCoreEndpoint({ ...validEndpoint, unexpected_status: true })).toThrow(
      /invalid endpoint/i,
    );

    const missingCurrent = charityModelFixture(3);
    delete (missingCurrent.prices as Record<string, unknown>).current_output_user_price_milli;
    expect(() => normalizeCharityModel(missingCurrent)).toThrow(/current_output_user_price_milli/i);
    expect(() =>
      normalizeCharityModel(charityModelFixture(3, { availability_reason: 'unknown' })),
    ).toThrow(/availability reason/i);
    expect(() =>
      normalizeCharityModel(
        charityModelFixture(3, { available: false, availability_reason: 'ok' }),
      ),
    ).toThrow(/availability reason/i);
    expect(() =>
      normalizeUpstreamModel({
        upstream_model_id: 'gpt',
        provider: 'p',
        fetched_at: 1,
        status: 'failed',
      }),
    ).toThrow(/upstream model status/i);
  });

  test('normalizes omitted reviews to an empty list but rejects invalid or zero timestamps', () => {
    const donation = {
      id: 1,
      endpoint_id: 2,
      endpoint_base_url: 'https://upstream.test/v1',
      status: 'pending',
      enabled: false,
      description: '',
      review_note: '',
      created_at: 1,
      updated_at: 2,
      keys: [],
    };
    expect(normalizeDonation(donation, true, 'openai-compatible').reviews).toEqual([]);
    expect(normalizeManagementDonation(donation, true).reviews).toEqual([]);
    expect(() =>
      normalizeDonation({ ...donation, reviews: {} }, true, 'openai-compatible'),
    ).toThrow(/review list/i);
    expect(() => normalizeManagementDonation({ ...donation, reviews: {} }, true)).toThrow(
      /review list/i,
    );
    expect(() =>
      normalizeDonation({ ...donation, created_at: 0 }, true, 'openai-compatible'),
    ).toThrow(/created timestamp/i);
    expect(() =>
      normalizeDonation({ ...donation, expires_at: 0 }, true, 'openai-compatible'),
    ).toThrow(/expiry timestamp/i);
    expect(() =>
      normalizeDonation(
        { ...donation, reviewed_at: '2026-08-23T00:00:00Z' },
        true,
        'openai-compatible',
      ),
    ).toThrow(/review timestamp/i);
  });

  test('keeps resource IDs opaque and validates only legacy numeric payload boundaries', () => {
    const invalidIDs: unknown[] = [
      '',
      '\u0001',
      'x'.repeat(129),
      0,
      -1,
      2.5,
      Number.MAX_SAFE_INTEGER + 1,
    ];
    for (const id of invalidIDs) {
      expect(() => normalizeEndpointKey(endpointKeyFixture(id), 'openai-compatible')).toThrow(
        /endpoint key id/i,
      );
      expect(() => normalizePlatformModel(platformModelFixture(id))).toThrow(/model id/i);
      expect(() => normalizeManagementCharityModel(charityModelFixture(id))).toThrow(
        /charity model id/i,
      );
    }
    expect(normalizeEndpointKey(endpointKeyFixture('model:2'), 'openai-compatible').id).toBe(
      'model:2',
    );
    expect(normalizePlatformModel(platformModelFixture('model:2')).id).toBe('model:2');
    expect(positiveDecimalIDNumber('2e3')).toBeUndefined();
    expect(positiveDecimalIDNumber('01')).toBeUndefined();
    expect(positiveDecimalIDNumber('2000')).toBe(2000);
    expect(
      normalizeEndpointKey(endpointKeyFixture('9007199254740991'), 'openai-compatible').id,
    ).toBe('9007199254740991');
  });
});
