import { mkdirSync } from 'node:fs';
import { join } from 'node:path';
import { expect, test } from './test';
import { ADMIN_ORIGIN, USER_ORIGIN } from './ports';
import { mockPublicConfig, mockRoleSession } from './support';
import type { AgentJob, AgentMessage, AgentPlan, Conversation } from '../../src/shared/agent/api';
import en from '../../src/shared/i18n/common/en.json' with { type: 'json' };
import zh from '../../src/shared/i18n/common/zh.json' with { type: 'json' };

for (const scenario of [
  { role: 'admin', language: 'zh', theme: 'light', width: 390 },
  { role: 'steward', language: 'en', theme: 'dark', width: 360 },
] as const) {
  test(`agent narrow tool details and plan decisions: ${scenario.role}`, async ({ page }) => {
    const station = scenario.role === 'admin' ? 'admin' : 'user';
    const origin = station === 'admin' ? ADMIN_ORIGIN : USER_ORIGIN;
    const root = station === 'admin' ? '/admin/api/ai' : '/api/steward/ai';
    const copy = (scenario.language === 'zh' ? zh : en).common.agent;
    await page.setViewportSize({ width: scenario.width, height: 844 });
    await page.addInitScript(({ language, theme }) => {
      localStorage.setItem('nb.lang', language);
      localStorage.setItem('nb.theme', theme);
    }, scenario);
    await mockPublicConfig(page, station);
    await mockRoleSession(page, station, station === 'admin' ? 'admin' : 'level6');
    const now = 1_800_000_000_000;
    const longModel = 'gateway/model-' + 'long-name-'.repeat(24);
    const messages: AgentMessage[] = [
      {
        id: '1',
        generation: '1',
        active: true,
        role: 'user',
        content: '查看捐赠，更新对话记忆。',
        created_at: now,
      },
      {
        id: '2',
        generation: '1',
        active: true,
        role: 'assistant',
        content: '',
        created_at: now,
        tool_calls: [
          {
            id: 'read-1',
            type: 'function',
            function: { name: 'donations', arguments: JSON.stringify({ query: longModel }) },
          },
        ],
      },
      {
        id: '3',
        generation: '1',
        active: true,
        role: 'tool',
        content: JSON.stringify({ items: [{ id: '42', model: longModel }] }),
        tool_call_id: 'read-1',
        created_at: now,
      },
      {
        id: '4',
        generation: '1',
        active: true,
        role: 'assistant',
        content: '',
        created_at: now,
        tool_calls: [
          {
            id: 'write-1',
            type: 'function',
            function: {
              name: 'edit_memory',
              arguments: JSON.stringify({
                action: 'write',
                kind: 'chat',
                content: 'updated',
                expected_revision: '1',
              }),
            },
          },
        ],
      },
    ];
    const job: AgentJob = {
      id: '9',
      kind: 'chat',
      actor_id: '1',
      actor_role: 'admin',
      actor_label: 'Preview',
      conversation_id: '1',
      generation: '1',
      donation_id: '0',
      state: 'waiting_confirmation',
      request_id: '1',
      error: '',
      created_at: now,
      updated_at: now,
      queue: { length: 0, active: 0, limit: 1, position: 0 },
    };
    const plan: AgentPlan = {
      id: '1',
      job_id: '9',
      state: 'pending',
      tool_call_ids: ['write-1'],
      created_at: now,
      operations: [
        {
          kind: 'memory.edit',
          target_id: 'chat',
          input: { kind: 'chat', content: 'updated', expected_revision: '1' },
        },
      ],
      preview: [
        {
          title: 'Update shared chat memory',
          description: '',
          before: '# 对话记忆\n保留资源编号。',
          after:
            '# 对话记忆\n保留资源编号。\n- 先查看工具回执，再确认操作结果。\n- 模型：' + longModel,
          dangerous: false,
          requires_elevation: false,
        },
      ],
    };
    const data: Conversation = {
      enabled: true,
      id: '1',
      generation: '1',
      messages,
      summary: '',
      plans: [plan],
      has_older: false,
      job,
    };
    const decisions: { approve: boolean; feedback: string }[] = [];
    await page.route('**/*', async (route) => {
      const path = new URL(route.request().url()).pathname;
      if (path === root + '/chat') return route.fulfill({ json: data });
      if (path === root + '/chat/plans/1/decision') {
        const input = route.request().postDataJSON();
        decisions.push(input);
        plan.state = input.approve ? 'approved' : 'rejected';
        plan.result = {
          status: input.approve ? 'completed' : 'rejected',
          feedback: input.feedback,
        };
        data.job = { ...job, state: 'completed' };
        data.messages = [
          ...messages,
          {
            id: '5',
            generation: '1',
            active: true,
            role: 'tool',
            content: JSON.stringify(plan.result),
            tool_call_id: 'write-1',
            created_at: now + 1,
          },
        ];
        return route.fulfill({ json: plan });
      }
      return route.fallback();
    });
    const errors: string[] = [];
    page.on('pageerror', (error) => errors.push(error.message));
    await page.goto(origin + (station === 'admin' ? '/ai' : '/steward/ai'));
    const fits = async (width: number = scenario.width) => {
      expect(
        await page.evaluate(() => ({
          width: innerWidth,
          scroll: document.documentElement.scrollWidth,
        })),
      ).toEqual({ width, scroll: width });
    };
    const evidence = async (name: string) => {
      if (!process.env.NONBIRI_VISUAL_DIR) return;
      mkdirSync(process.env.NONBIRI_VISUAL_DIR, { recursive: true });
      await page.screenshot({
        animations: 'disabled',
        path: join(
          process.env.NONBIRI_VISUAL_DIR,
          `agent-${scenario.role}-${scenario.width}-${name}.png`,
        ),
      });
    };
    const readCard = page.locator('.agent-tool-activity').first();
    await expect(readCard).toContainText(copy.activity.states.returned);
    await readCard.scrollIntoViewIfNeeded();
    await fits();
    await evidence('tools');
    await readCard.locator('summary').press('Enter');
    await expect(readCard.locator('pre').last()).toContainText(longModel);
    await fits();
    await evidence('tool-detail');
    await readCard.locator('summary').click();
    const pending = page.getByRole('region', { name: copy.plan });
    await expect(pending).toBeVisible();
    await pending.scrollIntoViewIfNeeded();
    await fits();
    await evidence('plan');
    await pending
      .getByRole('textbox', { name: copy.feedback })
      .fill('Please revise this proposal.');
    await pending.getByRole('button', { name: copy.rejectPlan }).click();
    await expect.poll(() => decisions.length).toBe(1);
    expect(decisions[0]).toEqual({ approve: false, feedback: 'Please revise this proposal.' });
    await expect(page.getByRole('button', { name: copy.approvePlan })).toHaveCount(0);
    await expect(page.locator('.agent-tool-activity').last()).toContainText(
      copy.activity.states.rejected,
    );
    await fits();

    // A fresh plan verifies both narrow-screen actions without discarding history.
    plan.state = 'pending';
    plan.result = undefined;
    data.job = job;
    data.messages = messages;
    await page.reload();
    await page.getByRole('button', { name: copy.approvePlan }).click();
    await expect.poll(() => decisions.length).toBe(2);
    expect(decisions[1]).toEqual({ approve: true, feedback: '' });
    await expect(page.locator('.agent-tool-activity').last()).toContainText(
      copy.activity.states.completed,
    );
    const historyPlan = page
      .locator('details')
      .filter({ has: page.locator('summary').filter({ hasText: copy.plan }) })
      .last();
    await historyPlan.locator(':scope > summary').click();
    await expect(historyPlan.getByRole('region', { name: copy.plan })).toBeVisible();
    await fits();
    await evidence('plan-completed');
    await page.getByRole('textbox', { name: copy.message }).fill('/compact 保留待确认操作');
    await expect(page.getByRole('button', { name: copy.compactNow })).toBeEnabled();
    await fits();
    await evidence('compact-command');

    plan.state = 'pending';
    plan.result = undefined;
    plan.operations = [
      {
        kind: 'charity.binding.apply_preset',
        target_id: '42',
        input: {
          channel_id: 'mch_example',
          expected_channel_revision: '1',
          expected_preset_revision: '1',
          expected_donation_revision: '1',
          models: [{ charity_model_id: '9', expected_binding_revision: '0' }],
        },
      },
    ];
    plan.preview = [
      {
        title: 'Apply preset',
        description: '',
        dangerous: false,
        requires_elevation: false,
        before: JSON.stringify({
          channel: { name: 'Example channel' },
          preset: {
            mappings: [
              {
                upstream_model_id: longModel,
                charity_model_id: '9',
                full_name: '[公益]example/model',
              },
            ],
          },
        }),
      },
    ];
    messages[1]!.tool_calls = [
      {
        id: 'read-1',
        type: 'function',
        function: { name: 'charity_binding_presets', arguments: '{"donation_id":"42"}' },
      },
    ];
    messages[3]!.tool_calls = [
      {
        id: 'write-1',
        type: 'function',
        function: {
          name: 'propose_operations',
          arguments: JSON.stringify({ operations: plan.operations }),
        },
      },
    ];
    data.messages = messages;
    data.job = job;
    await page.reload();
    await expect(page.getByText(copy.tools.charity_binding_presets, { exact: true })).toBeVisible();
    const presetPlan = page.getByRole('region', { name: copy.plan });
    await expect(presetPlan).toContainText(copy.operationNames.applyPreset);
    await expect(presetPlan).toContainText(copy.targetDonation.replace('{{id}}', '42'));
    await expect(presetPlan).toContainText(copy.presetSkips);
    await presetPlan
      .getByText(copy.presetMappings.replace('{{count}}', '1'), { exact: true })
      .click();
    await expect(presetPlan).toContainText(longModel);
    for (const width of [1440, 390]) {
      for (const theme of ['light', 'dark']) {
        await page.setViewportSize({ width, height: 900 });
        await page.evaluate((value) => {
          document.documentElement.dataset.theme = value;
        }, theme);
        await presetPlan.scrollIntoViewIfNeeded();
        await fits(width);
        await evidence(`preset-${width}-${theme}`);
      }
    }
    await presetPlan.getByRole('button', { name: copy.approvePlan }).click();
    await expect.poll(() => decisions.length).toBe(3);
    expect(decisions[2]).toEqual({ approve: true, feedback: '' });
    expect(errors).toEqual([]);
  });
}
