import { useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { Field, Note, Toggle } from '@shared/components/ui';
import { ErrorState, LoadingState } from '@shared/components/States';
import { useRetainedOperation } from '@shared/operations/useRetainedOperation';
import { useAIText, type AIBot } from '@shared/aiPlayers';
import { gameRequest } from '../../../user/games/common/request';
import presets from '../../../user/games/gwent/presets.json';
import './ai.css';

interface Policy {
  id: string;
  name: string;
  description: string;
  enabled: boolean;
  revision: string;
  version: number;
  definition: { schema: 'gwent-local/v1'; faction: string; preset: string };
}
interface State {
  settings: { enabled: boolean; revision: string };
  policies: Policy[];
  bots: AIBot[];
}
type Edit = { bot: AIBot; policy: Policy };
type Input = { kind: 'settings'; settings: State['settings'] } | { kind: 'bot'; value: Edit };
const root = '/admin/api/games/gwent/ai';
async function post(kind: string, body: unknown, key: string, signal: AbortSignal) {
  return (
    await gameRequest<unknown>(root + '/' + kind, {
      method: 'POST',
      json: body,
      idempotencyKey: key,
      signal,
      expectedStatuses: [200],
    })
  ).data;
}
export function GwentAIAdmin() {
  const t = useAIText();
  const query = useQuery({
    queryKey: ['admin', 'games', 'gwent', 'ai'],
    queryFn: async ({ signal }) =>
      (await gameRequest<State>(root, { signal, expectedStatuses: [200] })).data!,
    retry: false,
  });
  const [edit, setEdit] = useState<Edit>();
  const save = useRetainedOperation<Input, unknown>(
    async (input, key, context) => {
      if (input.kind === 'settings') return post('settings', input.settings, key, context.signal);
      const { bot, policy } = input.value;
      const updated = (await post(
        'policies',
        {
          id: policy.id,
          expected_revision: policy.revision,
          name: policy.name,
          description: policy.description,
          enabled: policy.enabled,
          definition: policy.definition,
        },
        key + '-policy',
        context.signal,
      )) as Policy;
      context.assertCurrent();
      return post(
        'bots',
        {
          id: bot.id,
          expected_revision: bot.revision,
          name: bot.name,
          description: bot.description,
          enabled: bot.enabled,
          policy_id: updated.id,
          policy_version: updated.version,
          ticket: bot.ticket,
          first_reward: bot.first_reward,
          memory_days: bot.memory_days,
          memory_games: bot.memory_games,
          new_challenge: false,
        },
        key + '-bot',
        context.signal,
      );
    },
    () => query.refetch(),
    ['admin', 'games', 'gwent', 'ai'],
  );
  const busy = save.isPending || save.outcome === 'unknown';
  if (query.isPending) return <LoadingState />;
  if (query.error) return <ErrorState error={query.error} onRetry={() => void query.refetch()} />;
  const state = query.data!;
  const update = (value: Partial<AIBot>) => {
    if (edit) setEdit({ ...edit, bot: { ...edit.bot, ...value } });
  };
  return (
    <section
      className="nb-panel ai-admin"
      aria-busy={busy}
      aria-label={t('昆特牌 AI 挑战', 'Gwent AI challenges')}
    >
      <Note>
        {t(
          '四个阵营使用原版本地评分器。卡组可选择本阵营原版预设。门票、首通奖励与竞标对决分别配置；没有逐局奖金。修改只影响新对局，不重置首通。',
          'Four factions use the original local scorer and their original deck presets. Tickets and first-clear rewards are independent of Bidding Duel. There are no per-match prizes. Changes apply to new matches and preserve first clears.',
        )}
      </Note>
      <Toggle
        label={t('开放昆特牌人机挑战', 'Enable Gwent AI challenges')}
        checked={state.settings.enabled}
        disabled={busy}
        onChange={(enabled) =>
          save.mutate({ kind: 'settings', settings: { ...state.settings, enabled } })
        }
      />
      {edit ? (
        <form
          onSubmit={(event) => {
            event.preventDefault();
            save.mutate({ kind: 'bot', value: edit }, { onSuccess: () => setEdit(undefined) });
          }}
        >
          <h3>
            {edit.policy.definition.faction.toUpperCase()} · {t('挑战设置', 'Challenge settings')}
          </h3>
          <Field label={t('名称', 'Name')}>
            {(props) => (
              <input
                {...props}
                value={edit.bot.name}
                maxLength={64}
                required
                disabled={busy}
                onChange={(e) => update({ name: e.target.value })}
              />
            )}
          </Field>
          <Field label={t('说明', 'Description')}>
            {(props) => (
              <textarea
                {...props}
                value={edit.bot.description}
                maxLength={512}
                disabled={busy}
                onChange={(e) => update({ description: e.target.value })}
              />
            )}
          </Field>
          <Field label={t('原版卡组预设', 'Original deck preset')}>
            {(props) => (
              <select
                {...props}
                disabled={busy}
                value={edit.policy.definition.preset}
                onChange={(e) =>
                  setEdit({
                    ...edit,
                    policy: {
                      ...edit.policy,
                      definition: { ...edit.policy.definition, preset: e.target.value },
                    },
                  })
                }
              >
                {presets
                  .filter((p) => p.deck.faction === edit.policy.definition.faction)
                  .map((p) => (
                    <option key={p.id} value={p.id}>
                      {p.name}
                    </option>
                  ))}
              </select>
            )}
          </Field>
          <p>
            {
              presets.find(
                (p) =>
                  p.deck.faction === edit.policy.definition.faction &&
                  p.id === edit.policy.definition.preset,
              )?.description
            }
          </p>
          <Field label={t('门票 · 积分', 'Ticket · credits')}>
            {(props) => (
              <input
                {...props}
                inputMode="decimal"
                value={edit.bot.ticket}
                required
                disabled={busy}
                onChange={(e) => update({ ticket: e.target.value })}
              />
            )}
          </Field>
          <Field label={t('首通奖励 · 游戏积分', 'First clear · game credits')}>
            {(props) => (
              <input
                {...props}
                inputMode="decimal"
                value={edit.bot.first_reward}
                required
                disabled={busy}
                onChange={(e) => update({ first_reward: e.target.value })}
              />
            )}
          </Field>
          <Toggle
            label={t('开放此挑战', 'Enable this challenge')}
            checked={edit.bot.enabled}
            disabled={busy}
            onChange={(enabled) => update({ enabled })}
          />
          <div className="nb-actions">
            <button type="submit" className="nb-btn nb-btn--primary" disabled={busy}>
              {t('保存', 'Save')}
            </button>
            <button
              className="nb-btn nb-btn--secondary"
              type="button"
              disabled={busy}
              onClick={() => setEdit(undefined)}
            >
              {t('取消', 'Cancel')}
            </button>
          </div>
        </form>
      ) : (
        <div className="ai-bot-list">
          {state.bots.map((bot) => {
            const policy = state.policies.find((p) => p.id === bot.policy_id);
            return (
              <article key={bot.id} className="nb-panel">
                <h3>{bot.name}</h3>
                <p>{bot.description}</p>
                <p>
                  {t('门票', 'Ticket')}: {bot.ticket} · {t('首通奖励', 'First clear')}:{' '}
                  {bot.first_reward}
                </p>
                <p>{bot.enabled ? t('已开放', 'Enabled') : t('未开放', 'Disabled')}</p>
                <button
                  className="nb-btn nb-btn--secondary"
                  disabled={busy || !policy}
                  onClick={() => {
                    if (policy) {
                      save.reset();
                      setEdit({ bot, policy });
                    }
                  }}
                >
                  {t('编辑挑战', 'Edit challenge')}
                </button>
              </article>
            );
          })}
        </div>
      )}
      {save.error && (
        <ErrorState
          error={save.error}
          onRetry={
            save.outcome === 'unknown'
              ? () => {
                  if (save.variables)
                    save.mutate(save.variables, { onSuccess: () => setEdit(undefined) });
                }
              : undefined
          }
        />
      )}
      {save.outcome === 'unknown' && (
        <Note>
          {t(
            '结果尚未确认，重试会继续同一次保存。',
            'The result is not confirmed. Retry continues the same save.',
          )}
        </Note>
      )}
    </section>
  );
}
