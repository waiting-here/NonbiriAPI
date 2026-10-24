import type { Page } from './test';
import { mockPublicConfig, mockRoleSession } from './support';
import { loanActivity } from '../fixtures/loans';

export const ALL_APPEARANCES = [
  'default',
  'celestial',
  'wandering',
  'heart-moment',
  'mirrorsea',
  'amber',
  'elopement',
  'heart-thief',
  'tomorrow',
];

const now = 1_800_000_000;
const pageOf = (data: unknown[]) => ({
  data,
  next_cursor: null,
  pagination: { page: '1', page_size: 20, total_items: String(data.length), total_pages: '1' },
});

export async function appearanceFixtures(page: Page, design: string) {
  await mockRoleSession(page, 'user', 'level6');
  await mockPublicConfig(page, 'user');
  await page.route('**/api/**', async (route) => {
    const path = new URL(route.request().url()).pathname;
    if (path === '/api/appearance')
      return route.fulfill({
        json: {
          selected: design,
          moment: 'cover:h',
          moments: ['cover:permanent', 'cover:h', 'cover:i'],
          unlocked: ALL_APPEARANCES,
          visible: ALL_APPEARANCES,
          updated_at: now,
        },
      });
    if (path === '/api/events')
      return route.fulfill({ contentType: 'text/event-stream', body: ': connected\n\n' });
    if (path === '/api/charity/models') {
      const catalog = new URL(route.request().url()).searchParams.has('page');
      return route.fulfill({
        json: catalog
          ? {
              models: [],
              pagination: pageOf([]).pagination,
              donation_intake: 'open',
              server_now: now,
            }
          : { state: 'no_models', models: [], donation_intake: 'open', server_now: now },
      });
    }
    if (path === '/api/donations')
      return route.fulfill({
        json: pageOf(
          ['expired', 'approved', 'deleted'].map((status, i) => ({
            id: String(i + 1),
            status,
            revision: '1',
            description: '捐赠服务 ' + (i + 1),
            review_result:
              status === 'approved' ? { decision: 'approve', reason: '', reviewed_at: now } : null,
            created_at: now,
            updated_at: now,
            discord_public_thanks: false,
            key_count: '1',
            source_count: '1',
            sources: [
              {
                kind: 'custom',
                connector_type: 'openai-compatible',
                base_url: 'https://controlled.example/v1',
              },
            ],
            state_counts: {
              rate_limited: '0',
              available: status === 'approved' ? '1' : '0',
              pending: '0',
              disabled: '0',
              suspended: '0',
              exhausted: '0',
              expired: status === 'expired' ? '1' : '0',
              ended: status === 'deleted' ? '1' : '0',
            },
          })),
        ),
      });
    if (path === '/api/caller-key')
      return route.fulfill({ json: null, headers: { 'X-Nonbiri-CallerKey-Generation': '0' } });
    if (['/api/models', '/api/endpoints', '/api/connections', '/api/announcements'].includes(path))
      return route.fulfill({ json: pageOf([]) });
    if (path.startsWith('/api/checkin'))
      return route.fulfill({
        json: {
          enabled: true,
          mutually_exclusive: true,
          blocked_by_other_checkin: false,
          asset_type: path.endsWith('/game') ? 'game' : 'general',
          checked_in_today: false,
          balance: '1981776.258',
          award_min: '4500',
          award_max: '5500',
          balance_cap: '250000',
        },
      });
    if (path === '/api/home/game-summary')
      return route.fulfill({ json: { continue: [], pending_results: [] } });
    if (path === '/api/activities') return route.fulfill({ json: loanActivity() });
    if (path === '/api/limited-activities') return route.fulfill({ json: [] });
    if (path === '/api/rewards/assets') return route.fulfill({ json: [] });
    if (path === '/api/rewards/history')
      return route.fulfill({ json: { items: [], next_cursor: '' } });
    if (path === '/api/credits/history')
      return route.fulfill({
        json: {
          data: [
            {
              operation_id: 'op_' + 'A'.repeat(22),
              line: 1,
              asset_type: 'general',
              kind: 'checkin_award',
              delta: '4500',
              created_at: now,
              request_id: null,
            },
          ],
          page: '1',
          page_size: 20,
          total: '1',
          total_pages: '1',
          anchor: 'op_' + 'A'.repeat(22),
          current_balance: '1981776.258',
          game_balance: '10000',
          server_now: now,
        },
      });
    if (path === '/api/limited-activities/picture-book/wallet')
      return route.fulfill({
        json: { general: '1981776.258', sketch_paper: '42', sketch_brush: '8' },
      });
    if (path === '/api/steward/maintenance')
      return route.fulfill({ json: { enabled: false, revision: '1' } });
    if (path === '/api/logs')
      return route.fulfill({
        json: pageOf([
          {
            id: 'req_' + 'A'.repeat(22),
            route_kind: 'openai_chat_completions',
            phase: 'handler',
            rejection_stage: null,
            rejection_reason: null,
            request_method: null,
            request_path: null,
            caller_result_class: 'success',
            caller_status: 200,
            caller_error_code: null,
            started_at: now,
            completed_at: now + 1,
            model: 'Saved model',
            attempt_count: '1',
            usage: {
              uncached_input_tokens: '0',
              cache_write_input_tokens: '0',
              cache_read_input_tokens: '0',
              output_tokens: '1',
              total_tokens: '1',
              usage_unknown: false,
              charge: '0',
            },
          },
        ]),
      });
    return route.fallback();
  });
}
