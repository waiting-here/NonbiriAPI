import { screen, within } from '@testing-library/react';
import { useLocation } from 'react-router';
import { expect, it, vi } from 'vitest';
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
  const managedRequest = { ...request, ...failedResult, charity_model_id: available ? '31' : null };
  const row: Record<string, unknown> =
    role === 'user' ? { ...request, ...failedResult, model: 'public model' } : managedRequest;
  const charityCaller = role === 'user' && managedRequest.route_kind.startsWith('charity_');
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
                    data: [1, 2].map((number) =>
                      role === 'user'
                        ? { ...attempt(number), endpoint_note: 'My service', key_note: 'My key' }
                        : {
                            ...attempt(number),
                            donation_id: available || number === 1 ? String(7 + number) : null,
                            donation_key_id: available || number === 1 ? String(11 + number) : null,
                          },
                    ),
                    next_cursor: null,
                  },
                  attempt_pagination: {
                    page: '1',
                    page_size: 20,
                    total_items: '2',
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
    const panel = await screen.findByRole('region', { name: /Log details/ });
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

it('explains missing historic targets and keeps ordinary charity logs free of management links', async () => {
  fixtures('admin', false);
  const view = await renderHistoricalLogs(<RoleLogPanel role="admin" accountId="viewer" />, {
    station: 'admin',
    role: 'admin',
    route: `/logs?page=2&request_id=${requestID}`,
  });
  const panel = await screen.findByRole('region', { name: /Log details/ });
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
    const panel = await screen.findByRole('region', { name: /Log details/ });
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
