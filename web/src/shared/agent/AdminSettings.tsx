import { useEffect, useId, useRef, useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';
import { LoadingState } from '@shared/components/States';
import {
  captureStationSession,
  stationSessionMatches,
  StationSessionChangedError,
} from '@shared/charityManagement';
import { Button, Field, Fold, Note, Panel, PanelBody, PanelHead } from '@shared/components/ui';
import {
  AGENT_TOOLS,
  agentRoot,
  discoverModels,
  getDriver,
  getSettings,
  putDriver,
  putSettings,
  type AgentSettings,
  type DriverSettings,
  type TemplateMessage,
} from './api';
import { AgentError } from './AgentParts';

const toolLabels: Record<string, string> = {
  donations: 'common.agent.tools.donations',
  donation_models: 'common.agent.tools.donation_models',
  review_history: 'common.agent.tools.review_history',
  charity_models: 'common.agent.tools.charity_models',
  charity_binding_presets: 'common.agent.tools.charity_binding_presets',
  users: 'common.agent.tools.users',
  site_status: 'common.agent.tools.site_status',
  web_search: 'common.agent.tools.web_search',
  propose_operations: 'common.agent.tools.propose_operations',
  edit_memory: 'common.agent.tools.edit_memory',
};
function useAdminRequestGuard() {
  const client = useQueryClient();
  const mounted = useRef(true);
  useEffect(() => {
    mounted.current = true;
    return () => {
      mounted.current = false;
    };
  }, []);
  return () => {
    const snapshot = captureStationSession(client, 'admin');
    const current = () => mounted.current && stationSessionMatches(client, 'admin', snapshot);
    const assertCurrent = () => {
      if (!current()) throw new StationSessionChangedError();
    };
    return { current, assertCurrent };
  };
}
function SecretField({
  hasKey,
  value,
  onChange,
  label,
}: {
  hasKey: boolean;
  value: string | undefined;
  onChange: (value: string | undefined) => void;
  label: string;
}) {
  const { t } = useTranslation();
  return (
    <Field label={label} help={t(hasKey ? 'common.agent.secretKeep' : 'common.agent.secretNew')}>
      {(props) => (
        <div className="nb-stack nb-stack--sm">
          <input
            {...props}
            type="password"
            autoComplete="new-password"
            value={value ?? ''}
            onChange={(event) => onChange(event.target.value || undefined)}
          />
          {hasKey ? (
            <div className="nb-actions">
              <Button
                type="button"
                variant="secondary"
                onClick={() => onChange(value === '' ? undefined : '')}
              >
                {t(value === '' ? 'common.agent.keepSecret' : 'common.agent.removeSecret')}
              </Button>
              {value === '' ? <span>{t('common.agent.secretRemoval')}</span> : null}
            </div>
          ) : null}
        </div>
      )}
    </Field>
  );
}
function TemplateEditor({
  value,
  onChange,
  chat,
}: {
  value: TemplateMessage[];
  onChange: (rows: TemplateMessage[]) => void;
  chat: boolean;
}) {
  const { t } = useTranslation();
  const patch = (index: number, row: Partial<TemplateMessage>) =>
    onChange(value.map((old, i) => (i === index ? { ...old, ...row } : old)));
  const move = (index: number, offset: number) => {
    const rows = value.slice();
    [rows[index], rows[index + offset]] = [rows[index + offset]!, rows[index]!];
    onChange(rows);
  };
  return (
    <div className="nb-stack">
      <p className="nb-small nb-muted">{t('common.agent.templateHelp')}</p>
      <ol className="agent-template-list">
        {value.map((row, index) => (
          <li key={index}>
            <div className="agent-template-row">
              {row.content === '{{conversation}}' ? (
                <strong>{t('common.agent.conversationNode')}</strong>
              ) : (
                <Field label={t('common.agent.messageRole')}>
                  {(props) => (
                    <select
                      {...props}
                      value={row.role}
                      onChange={(event) =>
                        patch(index, { role: event.target.value as TemplateMessage['role'] })
                      }
                    >
                      {(['system', 'user', 'assistant'] as const).map((role) => (
                        <option key={role} value={role}>
                          {t(
                            {
                              system: 'common.agent.roles.system',
                              user: 'common.agent.templateUser',
                              assistant: 'common.agent.roles.assistant',
                            }[role],
                          )}
                        </option>
                      ))}
                    </select>
                  )}
                </Field>
              )}
              <div className="nb-actions">
                <Button
                  type="button"
                  variant="ghost"
                  disabled={index === 0}
                  onClick={() => move(index, -1)}
                >
                  {t('common.connections.moveUp')}
                </Button>
                <Button
                  type="button"
                  variant="ghost"
                  disabled={index === value.length - 1}
                  onClick={() => move(index, 1)}
                >
                  {t('common.connections.moveDown')}
                </Button>
                <Button
                  type="button"
                  variant="ghost"
                  onClick={() => onChange(value.filter((_, i) => i !== index))}
                >
                  {t('common.remove')}
                </Button>
              </div>
            </div>
            {row.content === '{{conversation}}' ? (
              <p className="nb-small nb-muted">{t('common.agent.conversationNodeHelp')}</p>
            ) : (
              <>
                <Field label={t('common.agent.templateContent')}>
                  {(props) => (
                    <textarea
                      {...props}
                      rows={3}
                      value={row.content}
                      readOnly={row.content === '{{conversation}}'}
                      onChange={(event) => patch(index, { content: event.target.value })}
                    />
                  )}
                </Field>
                <div className="nb-actions">
                  <Button
                    type="button"
                    variant="secondary"
                    onClick={() => patch(index, { content: row.content + '\n{{memory}}' })}
                  >
                    {t('common.agent.insertMemory')}
                  </Button>
                  {!chat ? (
                    <Button
                      type="button"
                      variant="secondary"
                      onClick={() => patch(index, { content: row.content + '\n{{intelligence}}' })}
                    >
                      {t('common.agent.insertIntelligence')}
                    </Button>
                  ) : null}
                </div>
              </>
            )}
          </li>
        ))}
      </ol>
      <div className="nb-actions">
        <Button
          type="button"
          variant="secondary"
          onClick={() => onChange([...value, { role: 'system', content: '' }])}
        >
          {t('common.agent.addMessage')}
        </Button>
        {chat && !value.some((row) => row.content === '{{conversation}}') ? (
          <Button
            type="button"
            variant="secondary"
            onClick={() => onChange([...value, { role: 'user', content: '{{conversation}}' }])}
          >
            {t('common.agent.insertConversation')}
          </Button>
        ) : null}
      </div>
    </div>
  );
}
function DriverForm({ initial, account }: { initial: DriverSettings; account: string }) {
  const { t } = useTranslation();
  const client = useQueryClient();
  const guard = useAdminRequestGuard();
  const [base, setBase] = useState(initial);
  const [draft, setDraft] = useState(initial);
  const [key, setKey] = useState<string>();
  const [extra, setExtra] = useState(JSON.stringify(initial.extra_parameters ?? {}, null, 2));
  const [models, setModels] = useState<string[]>([]);
  const [reading, setReading] = useState(false);
  const [error, setError] = useState<unknown>(null);
  const patch = (change: Partial<DriverSettings>) => setDraft((old) => ({ ...old, ...change }));
  const dirty =
    key !== undefined ||
    JSON.stringify(draft) !== JSON.stringify(base) ||
    extra !== JSON.stringify(base.extra_parameters ?? {}, null, 2);
  const save = useMutation({
    mutationFn: async () => {
      const request = guard();
      const body: Omit<DriverSettings, 'revision' | 'has_key'> = {
        output_token_parameter: draft.output_token_parameter,
        base_url: draft.base_url,
        model: draft.model,
        context_window: draft.context_window,
        max_output_tokens: draft.max_output_tokens,
        temperature: draft.temperature,
        top_p: draft.top_p,
        extra_parameters: draft.extra_parameters,
        concurrency: draft.concurrency,
        timeout_seconds: draft.timeout_seconds,
      };
      const parameters: unknown = JSON.parse(extra);
      if (!parameters || typeof parameters !== 'object' || Array.isArray(parameters))
        throw new Error(t('common.agent.extraObject'));
      const value = await putDriver({
        ...body,
        extra_parameters: parameters as Record<string, unknown>,
        expected_revision: base.revision,
        ...(key !== undefined ? { api_key: key } : {}),
      });
      request.assertCurrent();
      setBase(value);
      setDraft(value);
      setExtra(JSON.stringify(value.extra_parameters ?? {}, null, 2));
      setKey(undefined);
      client.setQueryData([...agentRoot('admin', account), 'driver'], value);
      return value;
    },
  });
  const loadModels = async () => {
    const request = guard();
    setReading(true);
    setError(null);
    let discovering = false;
    try {
      if (dirty) await save.mutateAsync();
      request.assertCurrent();
      discovering = true;
      const models = await discoverModels();
      request.assertCurrent();
      setModels(models);
    } catch (failure) {
      if (request.current() && discovering) setError(failure);
    } finally {
      if (request.current()) setReading(false);
    }
  };
  const reload = async () => {
    const request = guard();
    setError(null);
    try {
      const current = await getDriver();
      request.assertCurrent();
      setBase(current);
      save.reset();
      client.setQueryData([...agentRoot('admin', account), 'driver'], current);
    } catch (failure) {
      if (request.current()) setError(failure);
    }
  };
  const busy = save.isPending || reading;
  return (
    <Panel>
      <PanelHead title={t('common.agent.driver')} description={t('common.agent.driverHelp')} />
      <PanelBody>
        <form
          className="nb-form"
          onSubmit={(event) => {
            event.preventDefault();
            save.mutate();
          }}
        >
          <fieldset className="agent-form-fields" disabled={busy}>
            <Field label={t('common.connections.entryURL')} help={t('common.agent.driverURLHelp')}>
              {(props) => (
                <input
                  {...props}
                  type="url"
                  value={draft.base_url}
                  onChange={(event) => patch({ base_url: event.target.value })}
                />
              )}
            </Field>
            <SecretField
              hasKey={base.has_key}
              value={key}
              onChange={setKey}
              label={t('common.connections.secret')}
            />
            <div className="agent-template-row">
              <Field label={t('common.connections.model')}>
                {(props) => (
                  <input
                    {...props}
                    list="agent-driver-models"
                    value={draft.model}
                    onChange={(event) => patch({ model: event.target.value })}
                  />
                )}
              </Field>
              <Button
                type="button"
                variant="secondary"
                disabled={busy || !draft.base_url || (!base.has_key && !key)}
                onClick={() => void loadModels()}
              >
                {t('common.agent.discoverModels')}
              </Button>
              <datalist id="agent-driver-models">
                {models.map((model) => (
                  <option key={model} value={model} />
                ))}
              </datalist>
            </div>
            <div className="agent-field-grid">
              <Field label={t('common.agent.contextWindow')}>
                {(props) => (
                  <input
                    {...props}
                    type="number"
                    min={1024}
                    step={1}
                    value={draft.context_window}
                    onChange={(event) => patch({ context_window: Number(event.target.value) })}
                  />
                )}
              </Field>
              <Field label={t('common.agent.maxOutput')}>
                {(props) => (
                  <input
                    {...props}
                    type="number"
                    min={1}
                    value={draft.max_output_tokens}
                    onChange={(event) => patch({ max_output_tokens: Number(event.target.value) })}
                  />
                )}
              </Field>
            </div>
            <Fold title={t('common.agent.advanced')}>
              <div className="agent-field-grid">
                <Field label={t('common.agent.tokenParameter')}>
                  {(props) => (
                    <select
                      {...props}
                      value={draft.output_token_parameter}
                      onChange={(event) => patch({ output_token_parameter: event.target.value })}
                    >
                      <option value="max_tokens">max_tokens</option>
                      <option value="max_completion_tokens">max_completion_tokens</option>
                    </select>
                  )}
                </Field>
                <Field label={t('common.agent.concurrency')}>
                  {(props) => (
                    <input
                      {...props}
                      type="number"
                      min={1}
                      max={128}
                      value={draft.concurrency}
                      onChange={(event) => patch({ concurrency: Number(event.target.value) })}
                    />
                  )}
                </Field>
                <Field label={t('common.agent.timeout')}>
                  {(props) => (
                    <input
                      {...props}
                      type="number"
                      min={1}
                      max={1200}
                      value={draft.timeout_seconds}
                      onChange={(event) => patch({ timeout_seconds: Number(event.target.value) })}
                    />
                  )}
                </Field>
                {(['temperature', 'top_p'] as const).map((name) => (
                  <Field
                    key={name}
                    label={t(
                      name === 'temperature' ? 'common.agent.temperature' : 'common.agent.topP',
                    )}
                    optional
                  >
                    {(props) => (
                      <input
                        {...props}
                        type="number"
                        min={0}
                        max={name === 'temperature' ? 2 : 1}
                        step="any"
                        value={draft[name] ?? ''}
                        onChange={(event) =>
                          patch({
                            [name]:
                              event.target.value === '' ? undefined : Number(event.target.value),
                          })
                        }
                      />
                    )}
                  </Field>
                ))}
              </div>
              <Field label={t('common.agent.extraParameters')} help={t('common.agent.extraHelp')}>
                {(props) => (
                  <textarea
                    {...props}
                    className="nb-mono"
                    rows={5}
                    value={extra}
                    onChange={(event) => setExtra(event.target.value)}
                  />
                )}
              </Field>
            </Fold>
            <div className="nb-actions">
              <Button variant="primary" type="submit" disabled={busy || !dirty}>
                {t('common.save')}
              </Button>
              <Button
                type="button"
                variant="secondary"
                disabled={busy}
                onClick={() => void reload()}
              >
                {t('common.agent.loadCurrent')}
              </Button>
              <Button
                type="button"
                variant="secondary"
                disabled={busy}
                onClick={() => {
                  setDraft(base);
                  setKey(undefined);
                  setExtra(JSON.stringify(base.extra_parameters ?? {}, null, 2));
                  save.reset();
                }}
              >
                {t('common.agent.restoreSaved')}
              </Button>
            </div>
            {save.error ? <AgentError error={save.error} /> : null}
            {error ? <AgentError error={error} /> : null}
            {save.isSuccess ? <Note tone="ok">{t('common.outcome.saved')}</Note> : null}
            <p className="nb-small nb-muted">{t('common.agent.configRecovery')}</p>
          </fieldset>
        </form>
      </PanelBody>
    </Panel>
  );
}
function AgentConfigForm({ initial, account }: { initial: AgentSettings; account: string }) {
  const { t } = useTranslation();
  const chatHelpId = useId();
  const client = useQueryClient();
  const guard = useAdminRequestGuard();
  const [base, setBase] = useState(initial);
  const [draft, setDraft] = useState(initial);
  const [key, setKey] = useState<string>();
  const [error, setError] = useState<unknown>(null);
  const patch = (change: Partial<AgentSettings>) => setDraft((old) => ({ ...old, ...change }));
  const save = useMutation({
    mutationFn: async () => {
      const request = guard();
      const body: Omit<AgentSettings, 'revision' | 'search_has_key'> = {
        chat_enabled: draft.chat_enabled,
        mode: draft.mode,
        auto_template: draft.auto_template,
        chat_template: draft.chat_template,
        auto_prefill: draft.auto_prefill,
        chat_prefill: draft.chat_prefill,
        tools: draft.tools,
        tool_descriptions: draft.tool_descriptions,
        search_url: draft.search_url,
      };
      const value = await putSettings({
        ...body,
        expected_revision: base.revision,
        ...(key !== undefined ? { search_api_key: key } : {}),
      });
      request.assertCurrent();
      setBase(value);
      setDraft(value);
      setKey(undefined);
      client.setQueryData([...agentRoot('admin', account), 'settings'], value);
      return value;
    },
  });
  const reload = async () => {
    const request = guard();
    setError(null);
    try {
      const current = await getSettings();
      request.assertCurrent();
      setBase(current);
      save.reset();
      client.setQueryData([...agentRoot('admin', account), 'settings'], current);
    } catch (failure) {
      if (request.current()) setError(failure);
    }
  };
  const busy = save.isPending;
  return (
    <Panel>
      <PanelHead title={t('common.agent.agentSettings')} />
      <PanelBody>
        <form
          className="nb-form"
          onSubmit={(event) => {
            event.preventDefault();
            save.mutate();
          }}
        >
          <fieldset className="agent-form-fields" disabled={busy}>
            <div>
              <label className="checkbox-label">
                <input
                  type="checkbox"
                  aria-describedby={chatHelpId}
                  checked={draft.chat_enabled}
                  onChange={(event) => patch({ chat_enabled: event.target.checked })}
                />
                <span>{t('common.agent.chatEnabled')}</span>
              </label>
              <p className="nb-small nb-muted" id={chatHelpId}>
                {t('common.agent.chatEnabledHelp')}
              </p>
            </div>
            <Field label={t('common.agent.autoMode')} help={t('common.agent.modeHelp')}>
              {(props) => (
                <select
                  {...props}
                  value={draft.mode}
                  onChange={(event) => patch({ mode: event.target.value as AgentSettings['mode'] })}
                >
                  <option value="off">{t('common.agent.modes.off')}</option>
                  <option value="preview">{t('common.agent.modes.preview')}</option>
                  <option value="auto">{t('common.agent.modes.auto')}</option>
                </select>
              )}
            </Field>
            <Fold title={t('common.agent.templates')}>
              <h3>{t('common.agent.reviewTemplate')}</h3>
              <TemplateEditor
                value={draft.auto_template}
                onChange={(auto_template) => patch({ auto_template })}
                chat={false}
              />
              <h3>{t('common.agent.chatTemplate')}</h3>
              <TemplateEditor
                value={draft.chat_template}
                onChange={(chat_template) => patch({ chat_template })}
                chat
              />
              <Field
                label={t('common.agent.reviewPrefill')}
                help={t('common.agent.prefillHelp')}
                optional
              >
                {(props) => (
                  <textarea
                    {...props}
                    value={draft.auto_prefill}
                    onChange={(event) => patch({ auto_prefill: event.target.value })}
                  />
                )}
              </Field>
              <Field label={t('common.agent.chatPrefill')} optional>
                {(props) => (
                  <textarea
                    {...props}
                    value={draft.chat_prefill}
                    onChange={(event) => patch({ chat_prefill: event.target.value })}
                  />
                )}
              </Field>
            </Fold>
            <Fold title={t('common.agent.toolsTitle')}>
              <p className="nb-small nb-muted">{t('common.agent.toolDescriptionHelp')}</p>
              <div className="agent-field-grid">
                {AGENT_TOOLS.map((name) => (
                  <div key={name} className="nb-stack nb-stack--sm">
                    <label className="checkbox-label">
                      <input
                        type="checkbox"
                        checked={draft.tools[name] ?? true}
                        onChange={(event) =>
                          patch({ tools: { ...draft.tools, [name]: event.target.checked } })
                        }
                      />
                      <span>{t(toolLabels[name]!)}</span>
                    </label>
                    <Fold title={t('common.agent.toolDescription')}>
                      <Field
                        label={t('common.agent.toolDescriptionFor', { tool: t(toolLabels[name]!) })}
                      >
                        {(props) => (
                          <textarea
                            {...props}
                            rows={6}
                            value={draft.tool_descriptions?.[name] ?? ''}
                            onChange={(event) =>
                              patch({
                                tool_descriptions: {
                                  ...draft.tool_descriptions,
                                  [name]: event.target.value,
                                },
                              })
                            }
                          />
                        )}
                      </Field>
                    </Fold>
                  </div>
                ))}
              </div>
            </Fold>
            <Fold title={t('common.agent.webSearch')}>
              <Field label={t('common.agent.searchURL')} help={t('common.agent.searchHelp')}>
                {(props) => (
                  <input
                    {...props}
                    type="url"
                    value={draft.search_url}
                    onChange={(event) => patch({ search_url: event.target.value })}
                  />
                )}
              </Field>
              <SecretField
                hasKey={base.search_has_key}
                value={key}
                onChange={setKey}
                label={t('common.agent.searchKey')}
              />
            </Fold>
            <div className="nb-actions">
              <Button
                variant="primary"
                type="submit"
                disabled={
                  save.isPending ||
                  (JSON.stringify(base) === JSON.stringify(draft) && key === undefined)
                }
              >
                {t('common.save')}
              </Button>
              <Button
                type="button"
                variant="secondary"
                disabled={save.isPending}
                onClick={() => void reload()}
              >
                {t('common.agent.loadCurrent')}
              </Button>
              <Button
                type="button"
                variant="secondary"
                disabled={busy}
                onClick={() => {
                  setDraft(base);
                  setKey(undefined);
                  save.reset();
                }}
              >
                {t('common.agent.restoreSaved')}
              </Button>
            </div>
            {save.error ? <AgentError error={save.error} /> : null}
            {error ? <AgentError error={error} /> : null}
            {save.isSuccess ? <Note tone="ok">{t('common.outcome.saved')}</Note> : null}
            <p className="nb-small nb-muted">{t('common.agent.configRecovery')}</p>
          </fieldset>
        </form>
      </PanelBody>
    </Panel>
  );
}
export function AdminSettings({ account, active }: { account: string; active: boolean }) {
  const driver = useQuery({
    queryKey: [...agentRoot('admin', account), 'driver'],
    queryFn: ({ signal }) => getDriver(signal),
    enabled: active,
    retry: false,
  });
  const settings = useQuery({
    queryKey: [...agentRoot('admin', account), 'settings'],
    queryFn: ({ signal }) => getSettings(signal),
    enabled: active,
    retry: false,
  });
  return (
    <div className="nb-stack">
      {driver.isPending ? (
        <LoadingState />
      ) : driver.error ? (
        <AgentError error={driver.error} onRetry={() => void driver.refetch()} />
      ) : (
        <DriverForm initial={driver.data} account={account} />
      )}
      {settings.isPending ? (
        <LoadingState />
      ) : settings.error ? (
        <AgentError error={settings.error} onRetry={() => void settings.refetch()} />
      ) : (
        <AgentConfigForm initial={settings.data} account={account} />
      )}
    </div>
  );
}
