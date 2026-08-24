import { fireEvent, screen, waitFor, within } from '@testing-library/react';
import { describe, expect, test, vi, type Mock } from 'vitest';
import backendCatalogCore from '../fixtures/site-config-catalog-core.json';
import { SettingsPage } from '../../src/admin/pages/SettingsPage';
import { GamesPage } from '../../src/admin/pages/GamesPage';
import { UsersPage } from '../../src/admin/pages/UsersPage';
import {
  normalizeAdminUser,
  normalizeSiteConfig,
  normalizeSiteConfigCatalog,
  type SiteConfigCatalogEntry,
  type SiteConfig,
} from '../../src/admin/data';
import { exactCreditDisplay, humanReadableSeconds } from '../../src/admin/utils/catalogDisplay';
import { normalizeAdminGameConfig, type AdminGameConfig } from '../../src/admin/features/games/data';
import {
  normalizeCharityModel,
  normalizeDonationKey,
  normalizeEndpointKey,
  normalizePlatformModel,
  normalizeUserSummary,
} from '../../src/user/data';
import {
  normalizeManagementCharityModel,
  normalizeManagementDonationKey,
} from '../../src/shared/charityManagement';
import { HomePage } from '../../src/user/pages/HomePage';
import { CharityPage } from '../../src/user/pages/CharityPage';
import { installJsonFetchFixtures, renderWithProviders } from './support';

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
    value_type: 'integer',
    title: { zh: `${key} 中文`, en: key === 'anthropic_default_max_tokens'
      ? 'Default Anthropic max output tokens'
      : 'Site timezone offset' },
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

function installSiteConfigServer(
  initial: SiteConfig,
  catalog: readonly SiteConfigCatalogEntry[],
  catalogResponse: unknown = { data: catalog },
): { fetchMock: Mock; state: SiteConfig; patches: Array<{ path: string; value: unknown }> } {
  const state = { ...initial };
  const patches: Array<{ path: string; value: unknown }> = [];
  const fetchMock = vi.fn(async (input: string | URL | Request, init?: RequestInit) => {
    const path = new URL(input instanceof Request ? input.url : String(input), window.location.origin).pathname;
    const method = (init?.method ?? (input instanceof Request ? input.method : 'GET')).toUpperCase();
    if (method === 'GET' && path === '/admin/api/site-config') return jsonResponse(state);
    if (method === 'GET' && path === '/admin/api/site-config/catalog') {
      return jsonResponse(catalogResponse);
    }
    if (method === 'PATCH' && path.startsWith('/admin/api/site-config/')) {
      const body = JSON.parse(String(init?.body)) as { value: unknown };
      const key = decodeURIComponent(path.slice('/admin/api/site-config/'.length));
      patches.push({ path, value: body.value });
      state[key] = body.value as SiteConfig[string];
      return jsonResponse({ key, value: body.value });
    }
    throw new Error(`Unexpected fixture request: ${method} ${path}`);
  });
  vi.stubGlobal('fetch', fetchMock);
  return { fetchMock, state, patches };
}

const baseUser = {
  id: 7,
  username: 'fixture-user',
  lang: 'en',
  is_banned: false,
  endpoint_limit: null,
  effective_endpoint_limit: 4,
  rpm_limit: null,
  effective_rpm_limit: 60,
  concurrency_limit: null,
  effective_concurrency_limit: 5,
  credits: '1000',
  donation_credit: '2000',
  effective_level: 2,
  manual_level: null,
  created_at: '2026-08-23T00:00:00Z',
};

describe('screenshot-facing configuration pages', () => {
  test('U1 removes only the internal level-computation implementation hint', async () => {
    installJsonFetchFixtures([
      { method: 'GET', path: '/api/session', body: { user: baseUser } },
      { method: 'GET', path: '/api/me', body: { user: baseUser } },
      { method: 'GET', path: '/api/me/usage', body: {
        total_requests: 1,
        total_prompt_tokens: 2,
        total_completion_tokens: 3,
        total_unknown_usage_requests: 0,
      } },
      { method: 'GET', path: '/api/checkin', body: { enabled: false } },
    ]);
    await renderWithProviders(<HomePage />, { station: 'user', locale: 'en', role: 'user' });

    expect(await screen.findByText('Current level')).toBeVisible();
    expect(screen.getByText('Lv2')).toBeVisible();
    expect(screen.queryByText(/This page computes nothing/i)).not.toBeInTheDocument();
  });

  test('U2 replaces the large call-status guide with a persistent neutral upstream warning', async () => {
    installJsonFetchFixtures([
      { method: 'GET', path: '/api/session', body: { user: baseUser } },
      { method: 'GET', path: '/api/charity/models', body: [] },
      { method: 'GET', path: '/api/donations', body: [] },
      { method: 'GET', path: '/api/endpoints', body: [] },
    ]);
    await renderWithProviders(<CharityPage />, { station: 'user', locale: 'en', role: 'user' });

    const warning = await screen.findByRole('note');
    expect(warning).toHaveTextContent('Third-party upstream privacy:');
    expect(warning).toHaveTextContent(/account logs may see the full request content/i);
    expect(warning).toHaveTextContent(/outside this site's control/i);
    expect(screen.queryByText('Call status guide')).not.toBeInTheDocument();
  });
});

describe('authoritative site-config frontend', () => {
  test('rejects non-canonical Anthropic integers and enforces the frozen timezone step', async () => {
    const anthropic = catalogEntry('anthropic_default_max_tokens', {
      value_type: 'optional_integer',
      nullable: true,
      null_writable: true,
      raw_default: null,
      effective_fallback: 65_536,
      minimum: 1,
      maximum: 2_147_483_647,
      step: 1,
      unit: { zh: 'Token', en: 'tokens' },
      zero_semantics: null,
      null_semantics: { zh: '使用内建值', en: 'Use built-in 65536' },
      empty_semantics: { zh: '留空重置', en: 'Empty form sends JSON null and resets the override' },
    });
    const timezone = catalogEntry('site_timezone_offset_minutes', {
      value_type: 'optional_integer',
      nullable: true,
      raw_default: null,
      effective_fallback: null,
      step: 30,
    });
    const server = installSiteConfigServer(
      { anthropic_default_max_tokens: null, site_timezone_offset_minutes: null },
      [anthropic, timezone],
    );
    const rendered = await renderWithProviders(<SettingsPage />, {
      station: 'admin', locale: 'en', role: 'admin',
    });

    const anthropicInput = await screen.findByLabelText('Default Anthropic max output tokens');
    const anthropicForm = anthropicInput.closest('form');
    expect(anthropicForm).not.toBeNull();
    const anthropicSave = within(anthropicForm!).getByRole('button');
    for (const invalid of ['1e3', '0', '1.5', '01', ' 1']) {
      fireEvent.change(anthropicInput, { target: { value: invalid } });
      await rendered.user.click(anthropicSave);
      expect(within(anthropicForm!).getByRole('alert')).toHaveTextContent(/canonical value/i);
    }
    expect(server.patches).toHaveLength(0);

    fireEvent.change(anthropicInput, { target: { value: '131072' } });
    await rendered.user.click(anthropicSave);
    await waitFor(() => expect(server.patches).toContainEqual({
      path: '/admin/api/site-config/anthropic_default_max_tokens', value: 131_072,
    }));
    fireEvent.change(anthropicInput, { target: { value: '' } });
    await rendered.user.click(within(anthropicForm!).getByRole('button', { name: 'Remove override' }));
    await waitFor(() => expect(server.patches.at(-1)?.value).toBeNull());

    const timezoneInput = screen.getByLabelText('Site timezone offset');
    const timezoneForm = timezoneInput.closest('form');
    expect(timezoneForm).not.toBeNull();
    for (const [raw, preview, numeric] of [
      ['-300', 'UTC-05:00', -300],
      ['0', 'UTC+00:00', 0],
      ['480', 'UTC+08:00', 480],
    ] as const) {
      fireEvent.change(timezoneInput, { target: { value: raw } });
      expect(within(timezoneForm!).getByText(`Preview: ${preview}`)).toBeVisible();
      await rendered.user.click(within(timezoneForm!).getByRole('button', { name: 'Save value' }));
      await waitFor(() => expect(server.patches.at(-1)?.value).toBe(numeric));
    }
    const patchCount = server.patches.length;
    fireEvent.change(timezoneInput, { target: { value: '345' } });
    await rendered.user.click(within(timezoneForm!).getByRole('button', { name: 'Save value' }));
    expect(within(timezoneForm!).getByRole('alert')).toHaveTextContent(/canonical value/i);
    expect(server.patches).toHaveLength(patchCount);
  });

  test('preserves only legal text losslessly and rejects malformed non-legal server values', () => {
    const exact = `  first\r\n\tsecond \n${'界'.repeat(100)}`;
    const legal = catalogEntry('legal_terms_override_en', {
      value_type: 'multiline_text', minimum: 0, maximum: 65_536,
    });
    expect(normalizeSiteConfig({ legal_terms_override_en: exact }, [legal]).legal_terms_override_en).toBe(exact);
    expect(() => normalizeSiteConfig({ legal_terms_override_en: '界'.repeat(21_846) }, [legal]))
      .toThrow(/legal_terms_override_en text/i);
    expect(() => normalizeSiteConfig({ legal_terms_override_en: 12.5 }, [legal]))
      .toThrow(/legal_terms_override_en text/i);

    const siteName = catalogEntry('site_name', {
      value_type: 'text', minimum: 0, maximum: 256,
    });
    const locale = catalogEntry('default_locale', {
      value_type: 'locale', minimum: null, maximum: null, allowed_values: ['zh', 'en'],
    });
    const rpm = catalogEntry('global_rpm', { minimum: 1, maximum: 4_096 });
    expect(() => normalizeSiteConfig({ site_name: 'x'.repeat(257) }, [siteName]))
      .toThrow(/site_name text/i);
    expect(() => normalizeSiteConfig({ site_name: 'bad\u0001name' }, [siteName]))
      .toThrow(/site_name text/i);
    expect(() => normalizeSiteConfig({ default_locale: 'fr' }, [locale]))
      .toThrow(/default_locale choice/i);
    expect(() => normalizeSiteConfig({ global_rpm: '100' }, [rpm]))
      .toThrow(/global_rpm integer/i);

    const first = catalogEntry('a');
    const second = catalogEntry('b');
    expect(normalizeSiteConfigCatalog({ data: [first, second] })).toHaveLength(2);
    expect(() => normalizeSiteConfigCatalog({ data: [second, first] })).toThrow(/key-sorted/i);
    expect(() => normalizeSiteConfigCatalog({ data: [{ ...first, title: { en: '', zh: '标题' } }] }))
      .toThrow(/catalog title/i);
  });

  test('consumes the backend-verified frozen core fixture with safe optional-field fallbacks', async () => {
    const normalized = normalizeSiteConfigCatalog(backendCatalogCore);
    expect(normalized).toHaveLength(1);
    expect(normalized[0]?.allowed_values).toBeUndefined();
    expect(normalized[0]?.independent_gates).toEqual([]);
    installSiteConfigServer({ site_name: 'Fixture' }, [], backendCatalogCore);
    await renderWithProviders(<SettingsPage />, {
      station: 'admin', locale: 'en', role: 'admin',
    });
    expect(await screen.findByLabelText('Site name')).toHaveValue('Fixture');
  });

  test('saves, refreshes, and resaves an exact 65536-byte legal document', async () => {
    const prefix = '  标题\r\n\tparagraph \r\n';
    const prefixBytes = new TextEncoder().encode(prefix).byteLength;
    const document = prefix + 'x'.repeat(65_536 - prefixBytes);
    expect(new TextEncoder().encode(document)).toHaveLength(65_536);
    const legal = catalogEntry('legal_terms_override_en', {
      value_type: 'multiline_text',
      title: { zh: '服务条款覆盖（英文）', en: 'Terms override (English)' },
      description: { zh: '逐字节保留', en: 'Preserved byte for byte' },
      unit: { zh: '无', en: 'none' },
      raw_default: '',
      effective_fallback: '',
      minimum: 0,
      maximum: 65_536,
      step: null,
    });
    const server = installSiteConfigServer({ legal_terms_override_en: document }, [legal]);
    const rendered = await renderWithProviders(<SettingsPage />, {
      station: 'admin', locale: 'en', role: 'admin',
    });
    const textarea = await screen.findByLabelText('Terms override (English)');
    const form = textarea.closest('form');
    expect(form).not.toBeNull();
    expect(within(form!).getByText('65536 / 65536 UTF-8 bytes')).toBeVisible();
    const save = within(form!).getByRole('button', { name: 'Save value' });
    await rendered.user.click(save);
    await waitFor(() => expect(server.patches).toHaveLength(1));
    expect(server.patches[0]?.path).toBe('/admin/api/site-config/legal_terms_override_en');
    expect(server.patches[0]?.value === document).toBe(true);

    const browserDocument = document.replaceAll('\r\n', '\n');
    const editedBrowserDocument = `${browserDocument.slice(0, -1)}y`;
    const editedDocument = `${document.slice(0, -1)}y`;
    fireEvent.change(textarea, { target: { value: editedBrowserDocument } });
    await rendered.user.click(save);
    await waitFor(() => expect(server.patches).toHaveLength(2));
    expect(server.patches[1]?.value === editedDocument).toBe(true);
    expect(String(server.patches[1]?.value).replaceAll('\r\n', '')).not.toContain('\n');

    fireEvent.change(textarea, { target: { value: browserDocument } });
    await rendered.user.click(save);
    await waitFor(() => expect(server.patches).toHaveLength(3));
    expect(server.patches[2]?.value === document).toBe(true);
  });

  test('renders exact milli-credit and human-readable seconds previews without float conversion', async () => {
    expect(humanReadableSeconds(90_061, 'en')).toBe('1d 1h 1m 1s');
    expect(humanReadableSeconds(90_061, 'zh')).toBe('1 天 1 小时 1 分钟 1 秒');
    expect(humanReadableSeconds(-3_661, 'en')).toBe('-1h 1m 1s');
    expect(exactCreditDisplay('9223372036854775807')).toBe('9223372036854775.807');
    expect(exactCreditDisplay('-1001')).toBe('-1.001');

    const milli = catalogEntry('credits_cap_milli', {
      value_type: 'amount', title: { zh: '签到积分门槛', en: 'Check-in credit threshold' },
      unit: { zh: '毫积分', en: 'milli-credits' }, raw_default: '0',
      effective_fallback: '0', minimum: '0', maximum: '9223372036854775807', step: '1',
    });
    const seconds = catalogEntry('rpm_ban_duration_seconds', {
      title: { zh: 'RPM 封禁时长', en: 'RPM auto-ban duration' },
      unit: { zh: '秒', en: 'seconds' }, raw_default: 3_661, effective_fallback: 3_661,
      minimum: 1, maximum: 86_400, step: 1,
    });
    installSiteConfigServer({
      credits_cap_milli: '9223372036854775807', rpm_ban_duration_seconds: 3_661,
    }, [milli, seconds]);
    const rendered = await renderWithProviders(<SettingsPage />, { station: 'admin', locale: 'en', role: 'admin' });
    expect(await screen.findByText(/Exact milli-credits: 9223372036854775807/)).toHaveTextContent(
      'Display credits: 9223372036854775.807',
    );
    expect(screen.getByText('Human-readable duration: 1h 1m 1s')).toBeVisible();
    await rendered.i18n.changeLanguage('zh');
    expect(await screen.findByText(/精确毫积分：9223372036854775807/)).toHaveTextContent(
      '展示积分：9223372036854775.807',
    );
    expect(screen.getByText('人类可读时长：1 小时 1 分钟 1 秒')).toBeVisible();
  });

  test('does not claim a timezone conflict restored authority when the refetch fails', async () => {
    const timezone = catalogEntry('site_timezone_offset_minutes', {
      value_type: 'optional_integer', nullable: true, raw_default: null,
      effective_fallback: null, step: 30,
    });
    let configReads = 0;
    vi.stubGlobal('fetch', vi.fn(async (input: string | URL | Request, init?: RequestInit) => {
      const path = new URL(input instanceof Request ? input.url : String(input), window.location.origin).pathname;
      const method = (init?.method ?? (input instanceof Request ? input.method : 'GET')).toUpperCase();
      if (method === 'GET' && path === '/admin/api/site-config/catalog') return jsonResponse({ data: [timezone] });
      if (method === 'GET' && path === '/admin/api/site-config') {
        configReads += 1;
        return configReads === 1
          ? jsonResponse({ site_timezone_offset_minutes: 0 })
          : jsonResponse({ error: { code: 'service_unavailable', message: 'refresh failed' } }, 503);
      }
      if (method === 'PATCH' && path.endsWith('/site_timezone_offset_minutes')) {
        return jsonResponse({ error: { code: 'conflict', message: 'timezone locked' } }, 409);
      }
      throw new Error(`Unexpected fixture request: ${method} ${path}`);
    }));
    const rendered = await renderWithProviders(<SettingsPage />, {
      station: 'admin', locale: 'en', role: 'admin',
    });
    const input = await screen.findByLabelText('Site timezone offset');
    fireEvent.change(input, { target: { value: '30' } });
    await rendered.user.click(within(input.closest('form')!).getByRole('button', { name: 'Save value' }));
    expect(await within(input.closest('form')!).findByRole('alert')).toHaveTextContent(
      /could not be refreshed/i,
    );
    expect(within(input.closest('form')!).getByRole('alert')).not.toHaveTextContent(/has been restored/i);
  });
});

describe('admin per-user limit explanations', () => {
  test('states the built-in concurrency default and independent gate composition', async () => {
    const adminUser = {
      id: 7, username: 'fixture-user', discord_id: 'discord', is_banned: false,
      banned_reason: '', endpoint_limit: null, effective_endpoint_limit: 4,
      rpm_limit: null, effective_rpm_limit: 60,
      concurrency_limit: null, effective_concurrency_limit: 5,
      total_requests: 0, total_prompt_tokens: 0, total_completion_tokens: 0,
      total_unknown_usage_requests: 0, credits_balance: '0', donation_credit_balance: '0',
      level: null, auto_level: 1, created_at: '2026-08-23T00:00:00Z',
    };
    installJsonFetchFixtures([{
      method: 'GET', path: '/admin/api/users?page=1&page_size=20',
      body: { data: [adminUser], has_more: false },
    }]);
    const rendered = await renderWithProviders(<UsersPage />, {
      station: 'admin', locale: 'en', role: 'admin',
    });
    await rendered.user.click(await screen.findByRole('button', { name: 'Manage' }));
    const note = screen.getByText(/built-in default of 5/i);
    expect(note).toHaveTextContent(/global RPM, global egress, endpoint and key gates/i);
    expect(note).toHaveTextContent(/effective is not the final minimum/i);
    await rendered.i18n.changeLanguage('zh');
    expect(await screen.findByText(/内建默认值 5/)).toHaveTextContent(/全站 RPM、全站出站并发、端点和密钥/);
    expect(screen.getByText(/内建默认值 5/)).toHaveTextContent(/不是所有门禁取最小后的最终上限/);
  });
});

const initialGameConfig: AdminGameConfig = {
  master_enabled: false,
  fishing: {
    enabled: false,
    bait_prices: { worm: '2500000', lure: '5000000', premium: '7500000' },
    rtp_percent: { standard: 90, premium: 88 },
    treasure_multipliers: { bottle: 2, clover: 3, shell: 5 },
  },
};

function cloneGameConfig(value: AdminGameConfig): AdminGameConfig {
  return structuredClone(value);
}

function installGameServer(options: { rejectPatch?: boolean } = {}) {
  const state = cloneGameConfig(initialGameConfig);
  const patches: unknown[] = [];
  const fetchMock = vi.fn(async (input: string | URL | Request, init?: RequestInit) => {
    const path = new URL(input instanceof Request ? input.url : String(input), window.location.origin).pathname;
    const method = (init?.method ?? (input instanceof Request ? input.method : 'GET')).toUpperCase();
    if (path !== '/admin/api/games/config') throw new Error(`Unexpected path ${path}`);
    if (method === 'GET') return jsonResponse(state);
    if (method !== 'PATCH') throw new Error(`Unexpected method ${method}`);
    const patch = JSON.parse(String(init?.body)) as Record<string, unknown>;
    patches.push(patch);
    if (options.rejectPatch) {
      return jsonResponse({ error: { code: 'invalid_request', message: 'Invalid fishing economy.' } }, 400);
    }
    const fishing = patch.fishing as Record<string, unknown> | undefined;
    if (typeof patch.master_enabled === 'boolean') state.master_enabled = patch.master_enabled;
    if (typeof fishing?.enabled === 'boolean') state.fishing.enabled = fishing.enabled;
    Object.assign(state.fishing.bait_prices, fishing?.bait_prices ?? {});
    Object.assign(state.fishing.rtp_percent, fishing?.rtp_percent ?? {});
    Object.assign(state.fishing.treasure_multipliers, fishing?.treasure_multipliers ?? {});
    // Prove that the page renders the returned full snapshot rather than the submitted draft.
    if ((fishing?.bait_prices as Record<string, unknown> | undefined)?.worm === '9007199254740993') {
      state.fishing.bait_prices.worm = '9007199254740994';
    }
    return jsonResponse(state);
  });
  vi.stubGlobal('fetch', fetchMock);
  return { fetchMock, patches, get state() { return state; } };
}

describe('standalone Admin Games feature', () => {
  test('uses exact price strings, touched-only patches, authoritative snapshots, and resets saved state on edit', async () => {
    const server = installGameServer();
    const rendered = await renderWithProviders(<GamesPage />, {
      station: 'admin', locale: 'en', role: 'admin',
    });
    const save = await screen.findByRole('button', { name: 'Save game configuration' });
    const worm = screen.getByLabelText('Worm bait');
    fireEvent.change(worm, { target: { value: '9007199254740993' } });
    await rendered.user.click(save);
    await waitFor(() => expect(server.patches).toHaveLength(1));
    expect(server.patches[0]).toEqual({ fishing: { bait_prices: { worm: '9007199254740993' } } });
    await waitFor(() => expect(screen.getByLabelText('Worm bait')).toHaveValue('9007199254740994'));
    expect(screen.getByRole('status')).toHaveTextContent(/authoritative server response/i);

    await rendered.user.click(save);
    expect(await screen.findByRole('alert')).toHaveTextContent(/canonical values/i);
    expect(screen.queryByRole('status')).not.toBeInTheDocument();

    fireEvent.change(screen.getByLabelText('Message bottle'), { target: { value: '7' } });
    expect(screen.queryByRole('status')).not.toBeInTheDocument();
    await rendered.user.click(save);
    await waitFor(() => expect(server.patches).toHaveLength(2));
    expect(server.patches[1]).toEqual({ fishing: { treasure_multipliers: { bottle: 7 } } });

    const edits: Array<[string, string | boolean, unknown]> = [
      ['Games master switch', true, { master_enabled: true }],
      ['Fishing enabled', true, { fishing: { enabled: true } }],
      ['Lure bait', '6000000', { fishing: { bait_prices: { lure: '6000000' } } }],
      ['Premium bait', '8000000', { fishing: { bait_prices: { premium: '8000000' } } }],
      ['Standard bait RTP', '91', { fishing: { rtp_percent: { standard: 91 } } }],
      ['Premium bait RTP', '89', { fishing: { rtp_percent: { premium: 89 } } }],
      ['Lucky clover', '4', { fishing: { treasure_multipliers: { clover: 4 } } }],
      ['Pearl shell', '6', { fishing: { treasure_multipliers: { shell: 6 } } }],
    ];
    for (const [label, value, expected] of edits) {
      const control = screen.getByLabelText(label);
      if (typeof value === 'boolean') await rendered.user.click(control);
      else fireEvent.change(control, { target: { value } });
      await rendered.user.click(save);
      await waitFor(() => expect(server.patches.at(-1)).toEqual(expected));
    }
  });

  test('does not guess a saved state after an invalid full-economy combination', async () => {
    const server = installGameServer({ rejectPatch: true });
    const rendered = await renderWithProviders(<GamesPage />, {
      station: 'admin', locale: 'en', role: 'admin',
    });
    const input = await screen.findByLabelText('Standard bait RTP');
    fireEvent.change(input, { target: { value: '100' } });
    await rendered.user.click(screen.getByRole('button', { name: 'Save game configuration' }));
    expect(await screen.findByText('Invalid fishing economy.')).toBeVisible();
    expect(screen.queryByRole('status')).not.toBeInTheDocument();
    expect(input).toHaveValue('100');
    expect(server.state).toEqual(initialGameConfig);
    fireEvent.change(screen.getByLabelText('Lucky clover'), { target: { value: '4' } });
    expect(screen.queryByText('Invalid fishing economy.')).not.toBeInTheDocument();
  });

  test('disables every editor while a late authoritative response is pending', async () => {
    let resolvePatch: ((response: Response) => void) | undefined;
    const pendingPatch = new Promise<Response>((resolve) => { resolvePatch = resolve; });
    vi.stubGlobal('fetch', vi.fn(async (input: string | URL | Request, init?: RequestInit) => {
      const method = (init?.method ?? (input instanceof Request ? input.method : 'GET')).toUpperCase();
      if (method === 'GET') return jsonResponse(initialGameConfig);
      return pendingPatch;
    }));
    const rendered = await renderWithProviders(<GamesPage />, {
      station: 'admin', locale: 'en', role: 'admin',
    });
    const worm = await screen.findByLabelText('Worm bait');
    fireEvent.change(worm, { target: { value: '3000000' } });
    await rendered.user.click(screen.getByRole('button', { name: 'Save game configuration' }));
    expect(worm).toBeDisabled();
    expect(screen.getByLabelText('Games master switch')).toBeDisabled();
    resolvePatch?.(jsonResponse({
      ...initialGameConfig,
      fishing: { ...initialGameConfig.fishing, bait_prices: {
        ...initialGameConfig.fishing.bait_prices, worm: '3000000',
      } },
    }));
    await waitFor(() => expect(screen.getByLabelText('Worm bait')).toBeEnabled());
    expect(screen.getByLabelText('Worm bait')).toHaveValue('3000000');
  });

  test('strictly rejects malformed GET fields instead of inventing game values', () => {
    expect(normalizeAdminGameConfig(initialGameConfig)).toEqual(initialGameConfig);
    expect(() => normalizeAdminGameConfig({ ...initialGameConfig, master_enabled: 0 })).toThrow(
      /master switch/i,
    );
    expect(() => normalizeAdminGameConfig({
      ...initialGameConfig,
      fishing: { ...initialGameConfig.fishing, bait_prices: {
        ...initialGameConfig.fishing.bait_prices, worm: '01',
      } },
    })).toThrow(/worm price/i);
  });
});

function completePrices() {
  return {
    request_user_price_milli: '0', request_donor_reward_milli: '0',
    uncached_user_price_milli: '0', cache_write_user_price_milli: '0',
    cache_read_user_price_milli: '0', output_user_price_milli: '0',
    uncached_donor_reward_milli: '0', cache_write_donor_reward_milli: '0',
    cache_read_donor_reward_milli: '0', output_donor_reward_milli: '0',
  };
}

describe('B1 and U3-U5 additive wire normalizers', () => {
  test('keeps nullable raw limits distinct from required effective limits', () => {
    const normalizedUser = normalizeUserSummary(baseUser);
    expect(normalizedUser).not.toHaveProperty('endpoint_limit');
    expect(normalizedUser).not.toHaveProperty('rpm_limit');
    expect(normalizedUser).not.toHaveProperty('concurrency_limit');
    expect(normalizedUser).toMatchObject({
      effective_endpoint_limit: 4, effective_rpm_limit: 60, effective_concurrency_limit: 5,
    });
    const admin = normalizeAdminUser({
      id: 7, username: 'fixture-user', discord_id: 'discord', is_banned: false,
      endpoint_limit: 50, effective_endpoint_limit: 50,
      rpm_limit: 4000, effective_rpm_limit: 4000,
      concurrency_limit: 70000, effective_concurrency_limit: 70000,
      total_requests: 0, total_prompt_tokens: 0, total_completion_tokens: 0,
      total_unknown_usage_requests: 0, credits_balance: '0', donation_credit_balance: '0',
      auto_level: 1, created_at: '2026-08-23T00:00:00Z',
    });
    expect(admin).toMatchObject({ endpoint_limit: 50, rpm_limit: 4000, concurrency_limit: 70000 });
    expect(() => normalizeAdminUser({ ...admin, effective_concurrency_limit: 100_001 }))
      .toThrow(/effective concurrency limit/i);
  });

  test('projects policy ownership fields and preserves strict donation limits and amounts', () => {
    expect(normalizeEndpointKey({ id: 1, force_store_false: true }).force_store_false).toBe(true);
    expect(normalizePlatformModel({ id: 2, flatten_tool_calls: true }).flatten_tool_calls).toBe(true);
    const modelFixture = {
      id: 3, enabled: true, flatten_tool_calls: true, pricing_mode: 'per_request',
      prices: completePrices(), discount: { percent: 100, enabled: false },
      success_samples: 0, success_count: 0,
    };
    expect(normalizeCharityModel(modelFixture).flatten_tool_calls).toBe(true);
    expect(normalizeManagementCharityModel(modelFixture).flatten_tool_calls).toBe(true);
    const keyFixture = {
      id: 4, max_concurrency: 100_000, rpm_limit: 4_096,
      credits_usage_cap_milli: '9007199254740993', credits_used_milli: '0',
      credits_reserved_milli: '0', enabled: true, force_store_false: true,
    };
    expect(normalizeDonationKey(keyFixture)).toMatchObject({
      max_concurrency: 100_000, rpm_limit: 4_096,
      credits_usage_cap_milli: '9007199254740993', force_store_false: true,
    });
    expect(normalizeManagementDonationKey(keyFixture).force_store_false).toBe(true);
    expect(() => normalizeEndpointKey({ id: 1, force_store_false: 1 })).toThrow(/store policy/i);
    expect(() => normalizePlatformModel({ id: 2, flatten_tool_calls: 'true' }))
      .toThrow(/tool-call policy/i);
    expect(() => normalizeDonationKey({ ...keyFixture, rpm_limit: 4_097 })).toThrow(/donation key RPM/i);
    expect(() => normalizeManagementDonationKey({
      ...keyFixture, credits_usage_cap_milli: '01',
    })).toThrow(/amount/i);
  });
});
