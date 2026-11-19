import { screen, within } from '@testing-library/react';
import { useLocation } from 'react-router';
import { expect, it, vi } from 'vitest';
import { DisplayTimeContext } from '@shared/components/timeContextValue';
import { renderHistoricalLogs } from '../../../../test/unit/logSupport';
import { installNativeDialog } from '../../../../test/unit/nativeDialog';
import { RoleLogPanel } from './RoleLogPanel';
import { logManagementReturnPath } from './managementNavigation';
import type { FailureGroup } from './managementFailures';

installNativeDialog();

const requestID = `req_${'A'.repeat(21)}Q`;
const usage = {
  uncached_input_tokens: '0',
  cache_write_input_tokens: '0',
  cache_read_input_tokens: '0',
  output_tokens: '1',
  total_tokens: '1',
  usage_unknown: false,
  charge: '0',
};
const request = {
  id: requestID,
  route_kind: 'charity_chat_completions',
  phase: 'handler',
  rejection_stage: null,
  rejection_reason: null,
  request_method: null,
  request_path: null,
  caller_result_class: 'success',
  caller_status: 200,
  caller_error_code: null,
  started_at: 1,
  completed_at: 2,
  usage,
  usage_total_mismatch: false,
  user_id: '7',
  caller_identity: null,
  attempt_count: '2',
  charity_model: 'A historical model name',
  charity_model_id: '31',
};
const attempt = (number: number) => ({
  attempt_seq: String(number),
  result_kind: 'response',
  endpoint_key_id: String(900 + number),
  donation_id: String(7 + number),
  donation_key_id: String(11 + number),
  endpoint_base_url: 'https://example.test/v1',
  connector_type: 'openai-compatible',
  upstream_model_id: 'upstream',
  status_code: 200,
  upstream_code: null,
  diag: null,
  usage,
  usage_total_mismatch: false,
  started_at: 1,
  completed_at: 2,
});

function LocationProbe() {
  const location = useLocation();
  return (
    <output data-testid="location">
      {JSON.stringify({ path: location.pathname + location.search, state: location.state })}
    </output>
  );
}

function fixtures(
  role: 'admin' | 'steward' | 'user',
  available = true,
  failure?: { summary: FailureGroup[]; suggestion?: string; routeKind?: string },
  attemptOverrides: readonly Record<string, unknown>[] = [{}, {}],
) {
  const failedResult = failure
    ? {
        caller_result_class: 'failed',
        caller_status: 400,
        caller_error_code: 'upstream',
        route_kind: failure.routeKind ?? request.route_kind,
        ...(failure.suggestion
          ? {
              rejection_detail: {
                field: 'model',
                reason: 'Upstream rejected the model',
                suggestion: failure.suggestion,
              },
            }
          : {}),
      }
    : {};
  const charity = (failure?.routeKind ?? request.route_kind).startsWith('charity_');
  const managedRequest = {
    ...request,
    ...failedResult,
    charity_model: charity ? request.charity_model : null,
    charity_model_id: charity && available ? '31' : null,
    attempt_count: String(attemptOverrides.length),
  };
  const row: Record<string, unknown> =
    role === 'user' ? { ...managedRequest, model: 'public model' } : managedRequest;
  const charityCaller = role === 'user' && charity;
  if (charityCaller) delete row.attempt_count;
  const root =
    role === 'admin' ? '/admin/api/logs' : role === 'steward' ? '/api/steward/logs' : '/api/logs';
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL) => {
      const url = new URL(String(input), 'https://fixture.test');
      const pagination = {
        page: url.searchParams.get('page') ?? '2',
        page_size: 20,
        total_items: '21',
        total_pages: '2',
      };
      const body = url.pathname.endsWith('/time-zones')
        ? { version: 'go1.26.6-zoneinfo', zones: ['UTC'] }
        : url.pathname === root
          ? { data: [row], next_cursor: null, pagination }
          : url.pathname === `${root}/${requestID}`
            ? charityCaller
              ? {
                  request: row,
                  caller_safe_result: { class: row.caller_result_class },
                  failure_summary: failure?.summary,
                }
              : {
                  request: row,
                  failure_summary: failure?.summary,
                  attempts: {
                    data: attemptOverrides.map((overrides, index) => {
                      const number = index + 1;
                      return {
                        ...attempt(number),
                        ...(role === 'user'
                          ? { endpoint_note: 'My service', key_note: 'My key' }
                          : {
                              donation_id:
                                charity && (available || number === 1) ? String(7 + number) : null,
                              donation_key_id:
                                charity && (available || number === 1) ? String(11 + number) : null,
                            }),
                        ...overrides,
                      };
                    }),
                    next_cursor: null,
                  },
                  attempt_pagination: {
                    page: '1',
                    page_size: 20,
                    total_items: String(attemptOverrides.length),
                    total_pages: '1',
                  },
                }
            : { error: { code: 'not_found', message: 'Not found.' } };
      return new Response(JSON.stringify(body), {
        status:
          url.pathname === root ||
          url.pathname === `${root}/${requestID}` ||
          url.pathname.endsWith('/time-zones')
            ? 200
            : 404,
        headers: { 'Content-Type': 'application/json' },
      });
    }),
  );
}

it.each(['admin', 'level6', 'level5'] as const)(
  'links %s requests and individual attempts to their stable targets',
  async (viewer) => {
    const role = viewer === 'admin' ? 'admin' : 'steward';
    fixtures(role);
    const original = `${role === 'admin' ? '/logs?' : '/steward?tab=logs&'}charity_model=historical&page=2&page_size=20&from=&to=&request_id=${requestID}&attempt_page=1&attempt_page_size=20`;
    const view = await renderHistoricalLogs(
      <>
        <RoleLogPanel role={role} accountId="viewer" />
        <LocationProbe />
      </>,
      { station: viewer === 'admin' ? 'admin' : 'user', role: viewer, route: original },
    );
    const modelPath = `${role === 'admin' ? '/charity?' : '/steward?tab=charity&'}charity_section=models&charity_model=31`;
    const modelLinks = await screen.findAllByRole('link', { name: 'Manage charity model' });
    expect(modelLinks[0]).toHaveAttribute('href', modelPath);
    const panel = await screen.findByRole('dialog', { name: /Log details/ });
    await view.user.click(within(panel).getByText('Service call attempts', { selector: 'strong' }));
    const keyLinks = await within(panel).findAllByRole('link', { name: 'Manage charity key' });
    expect(keyLinks[0]).toHaveAttribute('href', modelPath + '&managed_donation=8&managed_key=12');
    expect(keyLinks[1]).toHaveAttribute('href', modelPath + '&managed_donation=9&managed_key=13');
    const before = JSON.parse(screen.getByTestId('location').textContent!).path;
    expect(new URL(before, 'https://test').searchParams.get('page')).toBe('2');
    await view.user.click(keyLinks[1]);
    expect(JSON.parse(screen.getByTestId('location').textContent!).state.logReturnTo).toBe(before);
  },
);

it.each(['admin', 'steward'] as const)(
  'shows %s the effect of each charity attempt and the historical retry time',
  async (role) => {
    const retryISO = '2023-11-14T22:14:21.000Z';
    const retryAt = Date.parse(retryISO) / 1000;
    const threshold = {
      connector_type: 'openai-compatible',
      streak_before: '9',
      streak_after: '10',
      threshold: '10',
    };
    fixtures(role, true, undefined, [
      {
        connector_type: 'google-gemini',
        native_service: 'gemini_generate_content',
        status_code: 503,
        error_diagnostics: { code: 'upstream_error', provider_reason: 'google_model_overloaded' },
        health_impact: {
          streak_effect: 'unchanged',
          streak_before: '9007199254740993',
          streak_after: '9007199254740993',
          backoff: { reason: 'google_model_overloaded', scope: 'service', retry_at: retryAt },
        },
      },
      {
        status_code: 400,
        health_impact: {
          streak_effect: 'incremented',
          streak_before: '10000',
          streak_after: '10001',
        },
      },
      { health_impact: { streak_effect: 'reset', streak_after: '0' } },
      {
        status_code: 429,
        health_impact: {
          streak_effect: 'pending',
          backoff: { reason: 'smart_quota', scope: 'key', period: 'week' },
        },
      },
      {
        status_code: 400,
        health_impact: {
          streak_effect: 'incremented',
          backoff: {
            reason: 'recorded_wait',
            scope: 'unknown',
            retry_at: retryAt,
            observed_only: true,
          },
        },
      },
      { status_code: 503 },
      {
        status_code: 400,
        failure_disablement: threshold,
        health_impact: { streak_effect: 'incremented', streak_before: '9', streak_after: '10' },
      },
    ]);
    const view = await renderHistoricalLogs(
      <DisplayTimeContext.Provider value={{ mode: 'site', offset_minutes: 480 }}>
        <RoleLogPanel role={role} accountId="viewer" />
      </DisplayTimeContext.Provider>,
      {
        station: role === 'admin' ? 'admin' : 'user',
        role: role === 'admin' ? 'admin' : 'level5',
        route: `${role === 'admin' ? '/logs?' : '/steward?tab=logs&'}request_id=${requestID}`,
      },
    );
    const panel = within(await screen.findByRole('dialog', { name: /Log details/ }));
    await view.user.click(await panel.findByText('Service call attempts', { selector: 'strong' }));
    const call = (number: number) =>
      within(panel.getByRole('heading', { name: `Attempt ${number}` }).closest('li')!);
    const noDisablement = 'This attempt did not trigger automatic disablement';

    const overloaded = call(1);
    expect(overloaded.getByText('Consecutive failures unchanged')).toBeVisible();
    expect(overloaded.getByText('9,007,199,254,740,993 → 9,007,199,254,740,993')).toBeVisible();
    expect(overloaded.getByText(noDisablement)).toBeVisible();
    const wait = overloaded.getByText('Wait triggered').closest<HTMLElement>('[role="alert"]')!;
    expect(within(wait).getByText('Google model overloaded')).toBeVisible();
    expect(overloaded.getAllByText('Google model overloaded')).toHaveLength(1);
    expect(wait).toHaveTextContent('Service on this key · Gemini GenerateContent');
    expect(wait.querySelector('time')).toHaveAttribute('datetime', retryISO);
    expect(wait.querySelector('time')).toHaveTextContent('11/15/2023, 06:14:21 AM (UTC+08:00)');

    expect(call(2).getByText('Consecutive failures increased')).toBeVisible();
    expect(call(2).getByText('10,000 → 10,001')).toBeVisible();
    expect(call(2).getByText(noDisablement)).toBeVisible();
    expect(call(3).getByText('Consecutive failures cleared')).toBeVisible();
    expect(call(3).getByText('After this call 0')).toBeVisible();
    expect(call(3).getByText(noDisablement)).toBeVisible();
    expect(call(4).getByText('Consecutive failures awaiting earlier calls')).toBeVisible();
    expect(call(4).getByText('Weekly allowance exhausted')).toBeVisible();
    expect(call(4).getByText('Whole key')).toBeVisible();
    expect(call(4).getByText('Retry time not recorded')).toBeVisible();
    expect(call(4).queryByText(noDisablement)).not.toBeInTheDocument();

    const historical = call(5);
    expect(historical.getByText('Consecutive failures increased')).toBeVisible();
    expect(historical.getByText('Recorded retry guidance')).toBeVisible();
    expect(historical.getByText('Wait reason not recorded')).toBeVisible();
    expect(historical.getByText('Scope not recorded')).toBeVisible();
    expect(historical.getByText('Suggested retry time')).toBeVisible();
    expect(historical.queryByText('Wait triggered')).not.toBeInTheDocument();
    expect(historical.queryByText(noDisablement)).not.toBeInTheDocument();
    expect(call(6).getByText('Consecutive failure change not recorded')).toBeVisible();
    expect(call(6).queryByText('Wait triggered')).not.toBeInTheDocument();
    expect(call(6).queryByText(noDisablement)).not.toBeInTheDocument();
    expect(call(7).getByText('Triggered automatic disablement')).toBeVisible();
    expect(
      call(7).getByText('Consecutive failures rose from 9 to 10 (disable threshold 10).'),
    ).toBeVisible();
    expect(call(7).queryByText('Consecutive failures increased')).not.toBeInTheDocument();
  },
);

it('refreshes a pending outcome in place when its earlier call settles', async () => {
  const outcomes: Record<string, unknown>[] = [{ health_impact: { streak_effect: 'pending' } }];
  fixtures('steward', true, undefined, outcomes);
  const view = await renderHistoricalLogs(<RoleLogPanel role="steward" accountId="viewer" />, {
    station: 'user',
    role: 'level5',
    route: `/steward?tab=logs&request_id=${requestID}`,
  });
  const panel = within(await screen.findByRole('dialog', { name: /Log details/ }));
  await view.user.click(await panel.findByText('Service call attempts', { selector: 'strong' }));
  const refresh = panel.getByRole('button', { name: 'Refresh pending outcomes' });
  outcomes[0] = {
    health_impact: { streak_effect: 'reset', streak_before: '3', streak_after: '0' },
  };
  await view.user.click(refresh);
  expect(await panel.findByText('3 → 0')).toBeVisible();
  expect(refresh).not.toBeInTheDocument();
});

it('leaves charity health observations out of personal attempts in management logs', async () => {
  fixtures('admin', false, { summary: [], routeKind: 'openai_chat_completions' }, [
    { health_impact: { streak_effect: 'reset', streak_after: '0' } },
  ]);
  const view = await renderHistoricalLogs(<RoleLogPanel role="admin" accountId="viewer" />, {
    station: 'admin',
    role: 'admin',
    route: `/logs?request_id=${requestID}`,
  });
  const panel = within(await screen.findByRole('dialog', { name: /Log details/ }));
  await view.user.click(await panel.findByText('Service call attempts', { selector: 'strong' }));
  expect(panel.getByRole('heading', { name: 'Attempt 1' })).toBeVisible();
  expect(panel.queryByText('Consecutive failures cleared')).not.toBeInTheDocument();
  expect(panel.queryByText('Consecutive failure change not recorded')).not.toBeInTheDocument();
});

it('explains missing historic targets and keeps ordinary charity logs free of management links', async () => {
  fixtures('admin', false);
  const view = await renderHistoricalLogs(<RoleLogPanel role="admin" accountId="viewer" />, {
    station: 'admin',
    role: 'admin',
    route: `/logs?page=2&request_id=${requestID}`,
  });
  const panel = await screen.findByRole('dialog', { name: /Log details/ });
  expect(
    await within(panel).findByText('This record has no link to a current charity model.'),
  ).toBeVisible();
  await view.user.click(within(panel).getByText('Service call attempts', { selector: 'strong' }));
  expect(
    await within(panel).findAllByText('This attempt has no link to a current donation key.'),
  ).toHaveLength(2);
  expect(screen.queryByRole('link', { name: /Manage charity/ })).not.toBeInTheDocument();
  view.unmount();
  fixtures('user');
  await renderHistoricalLogs(<RoleLogPanel role="user" accountId="viewer" />, {
    station: 'user',
    role: 'user',
    route: `/logs?page=2&request_id=${requestID}`,
  });
  expect(await screen.findByText('public model', { selector: '.mono' })).toBeVisible();
  expect(screen.queryByRole('link', { name: /Manage charity/ })).not.toBeInTheDocument();
});

const authFailures = [
  { reason: 'google_access_token_type_unsupported', status_code: 401, count: 8 },
];
const quotaFailures = [{ reason: 'google_daily_requests', status_code: 429, count: 5 }];
const preciseSuggestion = 'Review authorization for the selected upstream model.';
const retryHint = 'Retry later, or add a second source to the model.';
const charityHint =
  'Try again later or choose another charity model. If the problem persists, include this request ID when reporting it.';

it.each([
  { role: 'admin', summary: [...authFailures, ...quotaFailures], expectedHint: null },
  {
    role: 'admin',
    summary: authFailures,
    suggestion: preciseSuggestion,
    expectedHint: preciseSuggestion,
  },
  { role: 'admin', summary: [], expectedHint: retryHint },
  { role: 'user', summary: authFailures, expectedHint: charityHint },
  { role: 'user', summary: [], routeKind: 'openai_chat_completions', expectedHint: retryHint },
] as const)(
  'shows scoped failure guidance for $role with $expectedHint',
  async ({ role, summary, expectedHint, ...details }) => {
    fixtures(role, true, { summary: [...summary], ...details });
    await renderHistoricalLogs(<RoleLogPanel role={role} accountId="viewer" />, {
      station: role === 'admin' ? 'admin' : 'user',
      role,
      route: `/logs?page=2&request_id=${requestID}`,
    });
    const panel = await screen.findByRole('dialog', { name: /Log details/ });
    await within(panel).findByText(/Provider error/);
    if (expectedHint) expect(within(panel).getByText(expectedHint)).toBeVisible();
    if (expectedHint !== retryHint) {
      expect(within(panel).queryByText(retryHint)).not.toBeInTheDocument();
    }
    if (role === 'admin' && summary.length > 0) {
      const grouping = within(panel).getByRole('region', { name: 'Failures across all attempts' });
      expect(within(grouping).getByText('8 attempts')).toBeVisible();
    } else {
      expect(
        within(panel).queryByRole('region', { name: 'Failures across all attempts' }),
      ).not.toBeInTheDocument();
    }
  },
);

it('accepts only matching station log return paths', () => {
  expect(logManagementReturnPath({ logReturnTo: '/logs?page=2' }, 'admin')).toBe('/logs?page=2');
  expect(logManagementReturnPath({ logReturnTo: '/steward?tab=logs&page=2' }, 'steward')).toBe(
    '/steward?tab=logs&page=2',
  );
  for (const value of ['https://evil.test/logs', '/charity', '/logs-other', '/logs?x=\n'])
    expect(logManagementReturnPath({ logReturnTo: value }, 'admin')).toBeUndefined();
  expect(
    logManagementReturnPath({ logReturnTo: '/steward?tab=charity' }, 'steward'),
  ).toBeUndefined();
});
