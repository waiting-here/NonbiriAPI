import { useState } from 'react';
import { useMutation, useQuery } from '@tanstack/react-query';
import { Field, Note, Toggle } from '@shared/components/ui';
import { ErrorState, LoadingState } from '@shared/components/States';
import { useRetainedOperation } from '@shared/operations/useRetainedOperation';
import {
  useAIText,
  type AIAdminState,
  type AIBot,
  type AIPolicy,
  type AIPolicyDefinition,
  type AIParameters,
  type AIRule,
} from '@shared/aiPlayers';
import { readAI, saveAI, previewAI } from './ai';
import './ai.css';

type NumericParameter = Exclude<keyof AIParameters, 'endgame_guard'>;
const limits: Record<NumericParameter, number> = {
  hand_value: 1.5,
  urgency: 1,
  exploration: 0.15,
  memory_weight: 0.85,
  joker_cost: 1,
};
const parameterNames: Record<NumericParameter, [string, string]> = {
  hand_value: ['保留强牌', 'Preserve strong cards'],
  urgency: ['追分倾向', 'Catch-up urgency'],
  exploration: ['近似选择随机度', 'Near-best exploration'],
  memory_weight: ['个性化权重上限', 'Maximum memory weight'],
  joker_cost: ['保留 Joker', 'Preserve Joker'],
};
type Text = ReturnType<typeof useAIText>;
const label = (key: NumericParameter, t: Text) => t(...parameterNames[key]);
function Parameters({
  value,
  onChange,
  override = false,
}: {
  value: Partial<AIParameters>;
  onChange: (value: Partial<AIParameters>) => void;
  override?: boolean;
}) {
  const t = useAIText();
  return (
    <div className="ai-parameters">
      {(Object.keys(limits) as NumericParameter[]).map((key) => (
        <Field
          key={key}
          label={label(key, t)}
          help={override ? t('留空沿用基础参数', 'Leave blank to inherit') : undefined}
        >
          {(props) => (
            <div className="ai-parameter-input">
              {!override && (
                <input
                  {...props}
                  type="range"
                  min="0"
                  max={limits[key]}
                  step="0.01"
                  value={value[key] ?? 0}
                  onChange={(e) => onChange({ ...value, [key]: Number(e.target.value) })}
                />
              )}
              <input
                {...(override ? props : { 'aria-label': label(key, t) })}
                type="number"
                min="0"
                max={limits[key]}
                step="0.01"
                value={value[key] ?? ''}
                onChange={(e) => {
                  const next = { ...value };
                  if (e.target.value === '') delete next[key];
                  else next[key] = Number(e.target.value);
                  onChange(next);
                }}
              />
            </div>
          )}
        </Field>
      ))}
      {override ? (
        <Field label={t('残局保胜', 'Protect certain endgame wins')}>
          {(props) => (
            <select
              {...props}
              value={value.endgame_guard === undefined ? 'inherit' : String(value.endgame_guard)}
              onChange={(e) => {
                const next = { ...value };
                if (e.target.value === 'inherit') delete next.endgame_guard;
                else next.endgame_guard = e.target.value === 'true';
                onChange(next);
              }}
            >
              <option value="inherit">{t('沿用', 'Inherit')}</option>
              <option value="true">{t('开启', 'On')}</option>
              <option value="false">{t('关闭', 'Off')}</option>
            </select>
          )}
        </Field>
      ) : (
        <Toggle
          label={t('残局保胜', 'Protect certain endgame wins')}
          description={t(
            '最后两轮优先保留可以确保获胜的出牌。',
            'In the last two rounds, prioritize bids that guarantee a win.',
          )}
          checked={value.endgame_guard ?? true}
          onChange={(endgame_guard) => onChange({ ...value, endgame_guard })}
        />
      )}
    </div>
  );
}
function RuleEditor({
  rule,
  onChange,
  onRemove,
  index,
  moveUp,
  moveDown,
}: {
  rule: AIRule;
  onChange: (v: AIRule) => void;
  onRemove: () => void;
  index: number;
  moveUp?: () => void;
  moveDown?: () => void;
}) {
  const t = useAIText();
  const fields: Record<string, string> = {
    phase: t('阶段（0 Joker / 1 出价）', 'Phase (0 Joker / 1 bid)'),
    round: t('轮数', 'Round'),
    cards: t('剩余手牌', 'Cards left'),
    pool: t('奖池分数', 'Pool points'),
    lead: t('领先分数', 'Score lead'),
  };
  return (
    <section className="ai-rule">
      <div className="ai-toolbar">
        <h4>
          {t('规则', 'Rule')} {index + 1}
        </h4>
        <button
          type="button"
          className="nb-btn nb-btn--secondary"
          aria-label={t('上移规则', 'Move rule up')}
          disabled={!moveUp}
          onClick={moveUp}
        >
          ↑
        </button>
        <button
          type="button"
          className="nb-btn nb-btn--secondary"
          aria-label={t('下移规则', 'Move rule down')}
          disabled={!moveDown}
          onClick={moveDown}
        >
          ↓
        </button>
        <button type="button" className="nb-btn nb-btn--secondary" onClick={onRemove}>
          {t('删除规则', 'Remove rule')}
        </button>
      </div>
      <label>
        {t('满足', 'Match')}{' '}
        <select
          value={rule.match}
          onChange={(e) => onChange({ ...rule, match: e.target.value as AIRule['match'] })}
        >
          <option value="all">{t('全部条件', 'All conditions')}</option>
          <option value="any">{t('任意条件', 'Any condition')}</option>
        </select>
      </label>
      {rule.conditions.map((condition, i) => (
        <div className="ai-condition" key={i}>
          <select
            aria-label={t('条件字段', 'Condition field')}
            value={condition.field}
            onChange={(e) =>
              onChange({
                ...rule,
                conditions: rule.conditions.map((c, n) =>
                  n === i
                    ? {
                        ...c,
                        field: e.target.value,
                        ...(e.target.value === 'phase' ? { operator: 'eq', value: 0 } : {}),
                      }
                    : c,
                ),
              })
            }
          >
            {Object.entries(fields).map(([key, name]) => (
              <option key={key} value={key}>
                {name}
              </option>
            ))}
          </select>
          <select
            aria-label={t('比较方式', 'Comparison')}
            disabled={condition.field === 'phase'}
            value={condition.operator}
            onChange={(e) =>
              onChange({
                ...rule,
                conditions: rule.conditions.map((c, n) =>
                  n === i ? { ...c, operator: e.target.value } : c,
                ),
              })
            }
          >
            {Object.entries({ gte: '≥', gt: '>', lte: '≤', lt: '<', eq: '=' }).map(
              ([key, name]) => (
                <option key={key} value={key}>
                  {name}
                </option>
              ),
            )}
          </select>
          <input
            aria-label={t('条件数值', 'Condition value')}
            type="number"
            value={condition.value}
            onChange={(e) =>
              onChange({
                ...rule,
                conditions: rule.conditions.map((c, n) =>
                  n === i ? { ...c, value: Number(e.target.value) } : c,
                ),
              })
            }
          />
          <button
            type="button"
            className="nb-btn nb-btn--secondary"
            disabled={rule.conditions.length === 1}
            onClick={() =>
              onChange({ ...rule, conditions: rule.conditions.filter((_, n) => n !== i) })
            }
          >
            {t('移除', 'Remove')}
          </button>
        </div>
      ))}
      <button
        type="button"
        className="nb-btn nb-btn--secondary"
        disabled={rule.conditions.length >= 4}
        onClick={() =>
          onChange({
            ...rule,
            conditions: [...rule.conditions, { field: 'round', operator: 'gte', value: 9 }],
          })
        }
      >
        {t('添加条件', 'Add condition')}
      </button>
      <Field label={t('候选范围', 'Candidate filter')}>
        {(props) => (
          <select
            {...props}
            value={rule.filter}
            onChange={(e) => onChange({ ...rule, filter: e.target.value })}
          >
            <option value="">{t('全部合法选择', 'All legal choices')}</option>
            <option value="lower_half">{t('较小的一半手牌', 'Lower half of hand')}</option>
            <option value="upper_half">{t('较大的一半手牌', 'Upper half of hand')}</option>
            <option value="non_losing">
              {t('不会输给对方剩余牌', 'Cannot lose to a remaining card')}
            </option>
          </select>
        )}
      </Field>
      <Parameters
        override
        value={rule.override}
        onChange={(override) => onChange({ ...rule, override })}
      />
    </section>
  );
}
type PolicyDraft = { kind: 'policy'; value: AIPolicy };
type BotDraft = { kind: 'bot'; value: AIBot; newChallenge: boolean };
type Editor = PolicyDraft | BotDraft | null;
export function AIPlayersAdmin() {
  const t = useAIText();
  const query = useQuery({
    queryKey: ['admin', 'games', 'ai'],
    queryFn: ({ signal }) => readAI(signal),
    retry: false,
  });
  const [editor, setEditor] = useState<Editor>(null);
  const save = useRetainedOperation<Parameters<typeof saveAI>[0], unknown>(
    (input, key, context) => saveAI(input, key, context.signal),
    () => query.refetch(),
    ['admin', 'games', 'ai'],
  );
  const busy = save.isPending || save.outcome === 'unknown';
  const submit = (input: Parameters<typeof saveAI>[0]) =>
    save.mutate(input, { onSuccess: () => setEditor(null) });
  const createBot = (data: AIAdminState) =>
    setEditor({
      kind: 'bot',
      newChallenge: false,
      value: {
        id: '',
        name: '',
        description: '',
        revision: '0',
        enabled: false,
        policy_id: data.policies[0]?.id ?? '',
        policy_version: data.policies[0]?.version ?? 1,
        challenge_id: '',
        ticket: '0',
        first_reward: '0',
        memory_days: 30,
        memory_games: 30,
      },
    });
  return (
    <section
      className="nb-panel"
      aria-label={t('AI 玩家与策略', 'AI players and strategies')}
      aria-busy={busy}
    >
      <div className="ai-admin">
        {query.isPending ? (
          <LoadingState />
        ) : query.error ? (
          <ErrorState error={query.error} onRetry={() => void query.refetch()} />
        ) : (
          query.data && (
            <>
              {editor ? (
                <>
                  <button
                    type="button"
                    className="nb-btn nb-btn--secondary"
                    disabled={busy}
                    onClick={() => {
                      setEditor(null);
                      save.reset();
                    }}
                  >
                    ← {t('返回列表', 'Back to list')}
                  </button>
                  {editor.kind === 'policy' ? (
                    <PolicyEditor
                      key={editor.value.id || 'new-policy'}
                      value={editor.value}
                      data={query.data}
                      disabled={busy}
                      onChange={(value) => setEditor({ kind: 'policy', value })}
                      onSave={() => {
                        const p = editor.value;
                        submit({
                          kind: 'policies',
                          body: {
                            id: p.id,
                            expected_revision: p.revision,
                            name: p.name,
                            description: p.description,
                            enabled: p.enabled,
                            definition: p.definition,
                          },
                        });
                      }}
                    />
                  ) : (
                    <BotEditor
                      key={editor.value.id || 'new-bot'}
                      draft={editor}
                      data={query.data}
                      disabled={busy}
                      onChange={setEditor}
                      onSave={() => {
                        const b = editor.value;
                        submit({
                          kind: 'bots',
                          body: {
                            id: b.id,
                            expected_revision: b.revision,
                            name: b.name,
                            description: b.description,
                            enabled: b.enabled,
                            policy_id: b.policy_id,
                            policy_version: b.policy_version,
                            ticket: b.ticket,
                            first_reward: b.first_reward,
                            memory_days: b.memory_days,
                            memory_games: b.memory_games,
                            new_challenge: editor.newChallenge,
                          },
                        });
                      }}
                    />
                  )}
                </>
              ) : (
                <>
                  <Toggle
                    label={t('开放人机挑战', 'Open AI challenges')}
                    description={t(
                      '同时受小游戏总开关与竞标对决开关控制。',
                      'Also requires the games master switch and Bidding Duel to be enabled.',
                    )}
                    checked={query.data.settings.enabled}
                    disabled={busy}
                    onChange={(enabled) =>
                      submit({
                        kind: 'settings',
                        body: { enabled, revision: query.data!.settings.revision },
                      })
                    }
                  />
                  <div className="ai-toolbar">
                    <h3>{t('AI 玩家', 'AI players')}</h3>
                    <button
                      className="nb-btn nb-btn--primary"
                      type="button"
                      disabled={busy || !query.data.policies.length}
                      onClick={() => createBot(query.data!)}
                    >
                      {t('新增玩家', 'Add player')}
                    </button>
                  </div>
                  {query.data.bots.map((b) => (
                    <article className="ai-list-row" key={b.id}>
                      <div>
                        <strong>{b.name}</strong>
                        <p>{b.description}</p>
                        <small>
                          {b.enabled ? t('已启用', 'Enabled') : t('已停用', 'Disabled')} ·{' '}
                          {t('门票', 'Ticket')} {b.ticket} · {t('首通', 'First clear')}{' '}
                          {b.first_reward} ·{' '}
                          {query.data!.policies.find((p) => p.id === b.policy_id)?.name} v
                          {b.policy_version}
                        </small>
                      </div>
                      <button
                        className="nb-btn nb-btn--secondary"
                        type="button"
                        disabled={busy}
                        onClick={() => setEditor({ kind: 'bot', value: b, newChallenge: false })}
                      >
                        {t('编辑', 'Edit')}
                      </button>
                    </article>
                  ))}
                  <div className="ai-toolbar">
                    <h3>{t('策略', 'Strategies')}</h3>
                    <button
                      className="nb-btn nb-btn--primary"
                      type="button"
                      disabled={busy}
                      onClick={() =>
                        setEditor({
                          kind: 'policy',
                          value: newPolicy(query.data!.presets[0]!.policy),
                        })
                      }
                    >
                      {t('新增策略', 'Add strategy')}
                    </button>
                  </div>
                  <Note>
                    {t(
                      '保存会发布一个新版本。AI 玩家需明确绑定该版本，已入场对局保持原策略。',
                      'Saving publishes a new version. Assign it explicitly to an AI player; active matches keep their original strategy.',
                    )}
                  </Note>
                  {query.data.policies.map((p) => (
                    <article className="ai-list-row" key={p.id}>
                      <div>
                        <strong>
                          {p.name} · v{p.version}
                        </strong>
                        <p>{p.description}</p>
                        <small>
                          {p.enabled ? t('可用', 'Available') : t('已停用', 'Disabled')}
                        </small>
                      </div>
                      <div className="ai-toolbar">
                        <button
                          className="nb-btn nb-btn--secondary"
                          type="button"
                          disabled={busy}
                          onClick={() => setEditor({ kind: 'policy', value: p })}
                        >
                          {t('编辑', 'Edit')}
                        </button>
                        <button
                          className="nb-btn nb-btn--secondary"
                          type="button"
                          disabled={busy}
                          onClick={() =>
                            setEditor({
                              kind: 'policy',
                              value: {
                                ...newPolicy(p.definition),
                                name: p.name + t('副本', ' copy'),
                                description: p.description,
                              },
                            })
                          }
                        >
                          {t('复制', 'Duplicate')}
                        </button>
                      </div>
                    </article>
                  ))}
                </>
              )}
            </>
          )
        )}
        {save.error && (
          <ErrorState
            error={save.error}
            onRetry={
              save.outcome === 'unknown'
                ? () => {
                    if (save.variables)
                      save.mutate(save.variables, { onSuccess: () => setEditor(null) });
                  }
                : undefined
            }
          />
        )}
        {save.outcome === 'unknown' && (
          <Note>
            {t(
              '保存结果尚未确认。重试会复用原请求。',
              'The save result is unconfirmed. Retrying reuses the original request.',
            )}
          </Note>
        )}
        {save.isSuccess && !editor && <p role="status">{t('已保存', 'Saved')}</p>}
      </div>
    </section>
  );
}
function newPolicy(definition: AIPolicyDefinition): AIPolicy {
  return {
    id: '',
    name: '',
    description: '',
    enabled: true,
    revision: '0',
    version: 0,
    source_id: 'bidding-local',
    schema_id: definition.schema,
    definition: structuredClone(definition),
  };
}
function PolicyEditor({
  value,
  data,
  disabled,
  onChange,
  onSave,
}: {
  value: AIPolicy;
  data: AIAdminState;
  disabled: boolean;
  onChange: (v: AIPolicy) => void;
  onSave: () => void;
}) {
  const t = useAIText();
  const [scenario, setScenario] = useState(data.scenarios[0]?.id ?? '');
  const signature = JSON.stringify([value.definition, scenario]);
  const preview = useMutation({
    mutationFn: async () => ({ signature, result: await previewAI(value.definition, scenario) }),
  });
  const analysis = preview.data?.signature === signature ? preview.data.result : undefined;
  const change = (p: AIPolicyDefinition) => {
    preview.reset();
    onChange({ ...value, definition: p });
  };
  const scene = data.scenarios.find((s) => s.id === scenario);
  const move = (i: number, d: number) => {
    const rules = [...value.definition.rules];
    [rules[i], rules[i + d]] = [rules[i + d]!, rules[i]!];
    change({ ...value.definition, rules });
  };
  return (
    <fieldset disabled={disabled} className="ai-editor">
      <h3>{value.id ? t('编辑策略', 'Edit strategy') : t('新增策略', 'New strategy')}</h3>
      <Field label={t('名称', 'Name')}>
        {(props) => (
          <input
            {...props}
            value={value.name}
            maxLength={64}
            required
            onChange={(e) => onChange({ ...value, name: e.target.value })}
          />
        )}
      </Field>
      <Field label={t('简介', 'Description')}>
        {(props) => (
          <input
            {...props}
            value={value.description}
            maxLength={512}
            onChange={(e) => onChange({ ...value, description: e.target.value })}
          />
        )}
      </Field>
      <Toggle
        label={t('启用策略', 'Enable strategy')}
        description={t(
          '停用后，绑定它的 AI 玩家不再接受新对局。',
          'Disabled strategies stop their assigned AI players from accepting new matches.',
        )}
        checked={value.enabled}
        onChange={(enabled) => onChange({ ...value, enabled })}
      />
      <div className="ai-toolbar">
        {data.presets.map((p) => (
          <button
            className="nb-btn nb-btn--secondary"
            type="button"
            key={p.id}
            onClick={() => {
              preview.reset();
              onChange({
                ...value,
                name: value.name || p.name,
                description: value.description || p.description,
                definition: structuredClone(p.policy),
              });
            }}
          >
            {p.name}
          </button>
        ))}
      </div>
      <Parameters
        value={value.definition.parameters}
        onChange={(parameters) =>
          change({ ...value.definition, parameters: parameters as AIParameters })
        }
      />
      <h4>{t('条件规则', 'Conditional rules')}</h4>
      <p>
        {t(
          '从上到下只应用第一条命中的规则。候选为空时恢复全部合法选择。',
          'Only the first matching rule applies. An empty filter restores all legal choices.',
        )}
      </p>
      {value.definition.rules.map((rule, i) => (
        <RuleEditor
          key={i}
          index={i}
          rule={rule}
          onChange={(rule) =>
            change({
              ...value.definition,
              rules: value.definition.rules.map((r, n) => (n === i ? rule : r)),
            })
          }
          onRemove={() =>
            change({ ...value.definition, rules: value.definition.rules.filter((_, n) => n !== i) })
          }
          moveUp={i > 0 ? () => move(i, -1) : undefined}
          moveDown={i + 1 < value.definition.rules.length ? () => move(i, 1) : undefined}
        />
      ))}
      <button
        className="nb-btn nb-btn--secondary"
        type="button"
        disabled={value.definition.rules.length >= 8}
        onClick={() =>
          change({
            ...value.definition,
            rules: [
              ...value.definition.rules,
              {
                match: 'all',
                conditions: [{ field: 'round', operator: 'gte', value: 9 }],
                override: {},
                filter: '',
              },
            ],
          })
        }
      >
        {t('添加规则', 'Add rule')}
      </button>
      <section className="ai-preview">
        <h4>{t('局面预览', 'Situation preview')}</h4>
        <Field label={t('示例局面', 'Example situation')}>
          {(props) => (
            <select
              {...props}
              value={scenario}
              onChange={(e) => {
                setScenario(e.target.value);
                preview.reset();
              }}
            >
              {data.scenarios.map((s) => (
                <option key={s.id} value={s.id}>
                  {s.name}
                </option>
              ))}
            </select>
          )}
        </Field>
        {scene && (
          <p>
            {t('轮数', 'Round')} {scene.observation.round} · {t('奖池', 'Pool')}{' '}
            {scene.observation.view.pool_points} · {t('手牌', 'Hand')}{' '}
            {scene.observation.view.hand_remaining[scene.observation.seat]?.join(', ')}
          </p>
        )}
        <button
          className="nb-btn nb-btn--secondary"
          type="button"
          disabled={preview.isPending}
          onClick={() => preview.mutate()}
        >
          {preview.isPending ? t('计算中', 'Calculating') : t('预览选择', 'Preview choices')}
        </button>
        {preview.error && <ErrorState error={preview.error} />}
        {analysis && (
          <>
            <p>
              {analysis.matched_rule < 0
                ? t('使用基础参数', 'Base parameters')
                : t(
                    `命中规则 ${analysis.matched_rule + 1}`,
                    `Matched rule ${analysis.matched_rule + 1}`,
                  )}
              {analysis.filter_empty
                ? ` · ${t('候选过滤为空，已恢复', 'Empty filter restored')}`
                : ''}
            </p>
            <table>
              <thead>
                <tr>
                  <th>{t('选择', 'Choice')}</th>
                  <th>{t('评分', 'Score')}</th>
                  <th>{t('选用概率', 'Selection probability')}</th>
                </tr>
              </thead>
              <tbody>
                {analysis.candidates.map((c) => (
                  <tr key={c.id}>
                    <th>
                      {c.id === 'use'
                        ? t('使用 Joker', 'Use Joker')
                        : c.id === 'keep'
                          ? t('保留 Joker', 'Keep Joker')
                          : c.id.replace('bid-', t('出 ', 'Bid '))}
                      {c.guaranteed_win ? ' ✓' : ''}
                    </th>
                    <td>{c.score.toFixed(3)}</td>
                    <td>
                      <progress max={1} value={c.probability} /> {(c.probability * 100).toFixed(1)}%
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </>
        )}
        <small>
          {t(
            '预览使用固定公开局面，不读取玩家记忆。评分和选用概率不代表胜率。',
            'Preview uses fixed public situations without player memory. Scores and selection probabilities are not win rates.',
          )}
        </small>
      </section>
      <button
        className="nb-btn nb-btn--primary"
        type="button"
        disabled={!value.name.trim()}
        onClick={onSave}
      >
        {t('保存策略版本', 'Save strategy version')}
      </button>
    </fieldset>
  );
}
function BotEditor({
  draft,
  data,
  disabled,
  onChange,
  onSave,
}: {
  draft: BotDraft;
  data: AIAdminState;
  disabled: boolean;
  onChange: (v: BotDraft) => void;
  onSave: () => void;
}) {
  const t = useAIText(),
    b = draft.value;
  const edit = (patch: Partial<AIBot>) => onChange({ ...draft, value: { ...b, ...patch } });
  return (
    <fieldset disabled={disabled} className="ai-editor">
      <h3>{b.id ? t('编辑 AI 玩家', 'Edit AI player') : t('新增 AI 玩家', 'New AI player')}</h3>
      <Field label={t('名称', 'Name')}>
        {(props) => (
          <input
            {...props}
            maxLength={64}
            value={b.name}
            required
            onChange={(e) => edit({ name: e.target.value })}
          />
        )}
      </Field>
      <Field label={t('公开简介', 'Public description')}>
        {(props) => (
          <input
            {...props}
            maxLength={512}
            value={b.description}
            onChange={(e) => edit({ description: e.target.value })}
          />
        )}
      </Field>
      <Toggle
        label={t('启用玩家', 'Enable player')}
        checked={b.enabled}
        onChange={(enabled) => edit({ enabled })}
      />
      <Field label={t('绑定策略版本', 'Assigned strategy version')}>
        {(props) => (
          <select
            {...props}
            value={`${b.policy_id}:${b.policy_version}`}
            onChange={(e) => {
              const [id, version] = e.target.value.split(':');
              edit({ policy_id: id!, policy_version: Number(version) });
            }}
          >
            {!data.policies.some((p) => p.id === b.policy_id && p.version === b.policy_version) && (
              <option value={`${b.policy_id}:${b.policy_version}`}>
                {data.policies.find((p) => p.id === b.policy_id)?.name} v{b.policy_version}
              </option>
            )}
            {data.policies.map((p) => (
              <option key={p.id} value={`${p.id}:${p.version}`}>
                {p.name} v{p.version}
                {p.enabled ? '' : ` · ${t('停用', 'Disabled')}`}
              </option>
            ))}
          </select>
        )}
      </Field>
      <div className="ai-parameters">
        {(['ticket', 'first_reward'] as const).map((key) => (
          <Field
            key={key}
            label={
              key === 'ticket'
                ? t('门票', 'Ticket')
                : t('首通奖励 · 游戏积分', 'First clear · game credits')
            }
          >
            {(props) => (
              <input
                {...props}
                inputMode="decimal"
                value={b[key]}
                onChange={(e) => edit({ [key]: e.target.value })}
              />
            )}
          </Field>
        ))}
        <Field label={t('记忆天数', 'Memory days')}>
          {(props) => (
            <input
              {...props}
              type="number"
              min={1}
              max={30}
              value={b.memory_days}
              onChange={(e) => edit({ memory_days: Number(e.target.value) })}
            />
          )}
        </Field>
        <Field label={t('记忆局数上限', 'Maximum remembered matches')}>
          {(props) => (
            <input
              {...props}
              type="number"
              min={1}
              max={100}
              value={b.memory_games}
              onChange={(e) => edit({ memory_games: Number(e.target.value) })}
            />
          )}
        </Field>
      </div>
      <Note>
        {t(
          '缩小记忆窗口会淘汰范围外的样本，之后扩大不会找回。价格、策略和名称的调整保留原有首通记录。',
          'Shrinking the memory window discards samples outside it; expanding it later does not restore them. Price, strategy and name changes preserve first-clear records.',
        )}
      </Note>
      {b.id && (
        <Toggle
          label={t('建立新挑战', 'Create a new challenge')}
          description={t(
            '保存后所有玩家均可重新获得一次首通奖励。',
            'After saving, every player becomes eligible for another first-clear reward.',
          )}
          checked={draft.newChallenge}
          onChange={(newChallenge) => onChange({ ...draft, newChallenge })}
        />
      )}
      <button
        className="nb-btn nb-btn--primary"
        type="button"
        disabled={!b.name.trim()}
        onClick={onSave}
      >
        {t('保存 AI 玩家', 'Save AI player')}
      </button>
    </fieldset>
  );
}
