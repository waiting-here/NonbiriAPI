import { useId, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Button, Field, Fold, Note } from '@shared/components/ui';
import { useOperation } from '@shared/operations/useOperation';
import { agentRoot } from './api';
import { AgentError } from './AgentParts';
import {
  changeProvider,
  deleteProfile,
  discoverProfileModels,
  getDrivers,
  profileUpdate,
  saveProfile,
  type DriverConfiguration,
  type DriverProfile,
  type DriverProvider,
  type ProfileUpdate,
} from './driversApi';

type Intent =
  | { action: 'save'; id: string | null; input: ProfileUpdate; previousIDs: string[] }
  | { action: 'delete'; id: string; revision: string };

export function DriverProfileEditor({
  initial,
  config,
  account,
  onConfiguration,
  onSelect,
  onClose,
}: {
  initial: DriverProfile;
  config: DriverConfiguration;
  account: string;
  onConfiguration: (value: DriverConfiguration) => void;
  onSelect: (id: string) => void;
  onClose: () => void;
}) {
  const { t } = useTranslation();
  const [base, setBase] = useState(initial);
  const [draft, setDraft] = useState(initial);
  const [extra, setExtra] = useState(
    JSON.stringify(initial.settings.extra_parameters ?? {}, null, 2),
  );
  const [key, setKey] = useState<string | undefined>();
  const [models, setModels] = useState<string[]>([]);
  const [error, setError] = useState<unknown>(null);
  const [current, setCurrent] = useState<DriverProfile | null>(null);
  const [candidates, setCandidates] = useState<DriverProfile[]>([]);
  const [deleting, setDeleting] = useState(false);
  const keyRef = useRef<string | undefined>(undefined);
  const listID = useId();
  const root = agentRoot('admin', account);
  const accept = (value: DriverProfile) => {
    setBase(value);
    setDraft(value);
    setExtra(JSON.stringify(value.settings.extra_parameters ?? {}, null, 2));
    setKey(undefined);
    keyRef.current = undefined;
    setCurrent(null);
    setCandidates([]);
  };
  const save = useOperation<Intent, string, DriverProfile | null>({
    authorityRoot: root,
    clearSecrets: () => {
      keyRef.current = undefined;
      setKey(undefined);
    },
    execute: async (intent, secret, _operationKey, context) => {
      if (intent.action === 'delete') {
        await deleteProfile(intent.id, intent.revision, context.signal);
        const latest = await getDrivers(context.signal);
        context.commit(() => {
          onConfiguration(latest);
          onClose();
        });
        return null;
      }
      const value = await saveProfile(
        intent.id,
        {
          ...intent.input,
          ...(secret === undefined ? {} : { api_key: secret }),
        },
        context.signal,
      );
      context.commit(() => accept(value));
      return value;
    },
    reconcile: async (intent, failure, context) => {
      const latest = await getDrivers(context.signal);
      context.commit(() => {
        onConfiguration(latest);
        if (!failure || intent.action === 'delete') return;
        if (intent.id)
          setCurrent(latest.profiles.find((profile) => profile.id === intent.id) ?? null);
        else
          setCandidates(
            latest.profiles.filter(
              (profile) =>
                !intent.previousIDs.includes(profile.id) &&
                profile.name === intent.input.name &&
                profile.settings.provider === intent.input.settings.provider,
            ),
          );
      });
      if (
        intent.action === 'delete' &&
        !latest.profiles.some((profile) => profile.id === intent.id)
      ) {
        context.commit(onClose);
        return { operationConfirmed: true };
      }
    },
  });
  const discover = useOperation<string, never, string[]>({
    authorityRoot: root,
    execute: async (id, _secret, _operationKey, context) => {
      const value = await discoverProfileModels(id, context.signal);
      context.commit(() => setModels(value));
      return value;
    },
    reconcile: () => undefined,
  });
  const read = useOperation<string, never, void>({
    authorityRoot: root,
    execute: async (id, _secret, _operationKey, context) => {
      const latest = await getDrivers(context.signal);
      context.commit(() => {
        onConfiguration(latest);
        setCurrent(latest.profiles.find((profile) => profile.id === id) ?? null);
      });
    },
    reconcile: () => undefined,
  });
  const busy = save.isPending || discover.isPending || read.isPending;
  const unresolved = save.outcome === 'unknown' || save.outcome === 'conflict';
  const dirty =
    key !== undefined ||
    JSON.stringify(draft) !== JSON.stringify(base) ||
    extra !== JSON.stringify(base.settings.extra_parameters ?? {}, null, 2);
  const field = <K extends keyof DriverProfile['settings']>(
    name: K,
    value: DriverProfile['settings'][K],
  ) => {
    setDraft({ ...draft, settings: { ...draft.settings, [name]: value } });
  };
  const write = async () => {
    const parsed: unknown = JSON.parse(extra);
    if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed))
      throw new Error(t('common.agent.extraObject'));
    const input = profileUpdate(
      { ...draft, settings: { ...draft.settings, revision: base.settings.revision } },
      parsed as Record<string, unknown>,
    );
    const secret = keyRef.current;
    keyRef.current = undefined;
    setKey(undefined);
    return save.run(
      {
        action: 'save',
        id: base.id || null,
        input,
        previousIDs: config.profiles.map((profile) => profile.id),
      },
      secret,
    );
  };
  const runSave = () => {
    setError(null);
    void write().catch(setError);
  };
  const loadModels = async () => {
    setError(null);
    try {
      const value = dirty || !base.id ? await write() : base;
      if (value) await discover.run(value.id);
    } catch (failure) {
      setError(failure);
    }
  };
  return (
    <form
      className="agent-form-fields"
      aria-label={t('common.agent.drivers.editSource')}
      onSubmit={(event) => {
        event.preventDefault();
        runSave();
      }}
    >
      <fieldset disabled={busy} className="agent-form-fields">
        <Field label={t('common.agent.drivers.name')}>
          {(props) => (
            <input
              {...props}
              value={draft.name}
              maxLength={200}
              required
              onChange={(event) => setDraft({ ...draft, name: event.target.value })}
            />
          )}
        </Field>
        <Field label={t('common.agent.drivers.provider')}>
          {(props) => (
            <select
              {...props}
              value={draft.settings.provider}
              onChange={(event) => {
                const provider = event.target.value as DriverProvider;
                setDraft({
                  ...draft,
                  name: ['Chat Completions API', 'Responses API', 'ChatGPT'].includes(draft.name)
                    ? provider === 'chatgpt'
                      ? 'ChatGPT'
                      : provider === 'responses'
                        ? 'Responses API'
                        : 'Chat Completions API'
                    : draft.name,
                  settings: changeProvider(draft.settings, provider),
                });
                setKey(undefined);
                keyRef.current = undefined;
                setExtra('{}');
                setModels([]);
              }}
            >
              <option value="chat_completions">Chat Completions API</option>
              <option value="responses">Responses API</option>
              <option value="chatgpt">ChatGPT</option>
            </select>
          )}
        </Field>
        {draft.settings.provider !== 'chatgpt' ? (
          <>
            <Field label={t('common.connections.entryURL')}>
              {(props) => (
                <input
                  {...props}
                  type="url"
                  value={draft.settings.base_url}
                  required
                  onChange={(event) => field('base_url', event.target.value)}
                />
              )}
            </Field>
            <Field
              label={t('common.connections.secret')}
              help={t(
                base.settings.has_key && base.settings.provider === draft.settings.provider
                  ? 'common.agent.secretKeep'
                  : 'common.agent.secretNew',
              )}
            >
              {(props) => (
                <input
                  {...props}
                  type="password"
                  autoComplete="new-password"
                  value={key ?? ''}
                  onChange={(event) => {
                    const value = event.target.value || undefined;
                    setKey(value);
                    keyRef.current = value;
                  }}
                />
              )}
            </Field>
            {base.settings.has_key && base.settings.provider === draft.settings.provider ? (
              <Button
                type="button"
                size="sm"
                onClick={() => {
                  const value = key === '' ? undefined : '';
                  setKey(value);
                  keyRef.current = value;
                }}
              >
                {t(key === '' ? 'common.agent.keepSecret' : 'common.agent.removeSecret')}
              </Button>
            ) : null}
            {key === '' ? <Note tone="warn">{t('common.agent.secretRemoval')}</Note> : null}
          </>
        ) : (
          <Note>{t('common.agent.drivers.chatgptSourceHelp')}</Note>
        )}
        <Field label={t('common.connections.model')} help={t('common.agent.drivers.modelHelp')}>
          {(props) => (
            <input
              {...props}
              list={listID}
              value={draft.settings.model}
              onChange={(event) => field('model', event.target.value)}
            />
          )}
        </Field>
        <datalist id={listID}>
          {models.map((model) => (
            <option key={model} value={model} />
          ))}
        </datalist>
        <div className="nb-actions">
          <Button type="button" disabled={unresolved} onClick={() => void loadModels()}>
            {t('common.agent.discoverModels')}
          </Button>
        </div>
        <Fold title={t('common.agent.advanced')}>
          <div className="agent-form-fields">
            <div className="agent-field-grid">
              <Field
                label={t('common.agent.contextWindow')}
                help={t('common.agent.drivers.contextHelp')}
              >
                {(props) => (
                  <input
                    {...props}
                    type="number"
                    min={1024}
                    max={10000000}
                    value={draft.settings.context_window}
                    onChange={(event) => field('context_window', Number(event.target.value))}
                  />
                )}
              </Field>
              <Field
                label={t('common.agent.drivers.outputReserve')}
                help={t('common.agent.drivers.reserveHelp')}
              >
                {(props) => (
                  <input
                    {...props}
                    type="number"
                    min={1}
                    value={draft.settings.output_reserve_tokens}
                    onChange={(event) => field('output_reserve_tokens', Number(event.target.value))}
                  />
                )}
              </Field>
            </div>
            {draft.settings.provider !== 'chatgpt' ? (
              <>
                <Field
                  label={t('common.agent.maxOutput')}
                  help={t('common.agent.drivers.outputHelp')}
                >
                  {(props) => (
                    <input
                      {...props}
                      type="number"
                      min={draft.settings.provider === 'chat_completions' ? 1 : 0}
                      value={draft.settings.max_output_tokens}
                      onChange={(event) => field('max_output_tokens', Number(event.target.value))}
                    />
                  )}
                </Field>
                {draft.settings.provider === 'chat_completions' ? (
                  <Field label={t('common.agent.tokenParameter')}>
                    {(props) => (
                      <select
                        {...props}
                        value={draft.settings.output_token_parameter}
                        onChange={(event) => field('output_token_parameter', event.target.value)}
                      >
                        <option value="max_tokens">max_tokens</option>
                        <option value="max_completion_tokens">max_completion_tokens</option>
                      </select>
                    )}
                  </Field>
                ) : null}
                <div className="agent-field-grid">
                  {(['temperature', 'top_p'] as const).map((name) => (
                    <Field
                      key={name}
                      label={t(
                        name === 'temperature' ? 'common.agent.temperature' : 'common.agent.topP',
                      )}
                    >
                      {(props) => (
                        <input
                          {...props}
                          type="number"
                          step="any"
                          value={draft.settings[name] ?? ''}
                          onChange={(event) =>
                            field(
                              name,
                              event.target.value === '' ? undefined : Number(event.target.value),
                            )
                          }
                        />
                      )}
                    </Field>
                  ))}
                </div>
              </>
            ) : null}
            <Field label={t('common.agent.timeout')}>
              {(props) => (
                <input
                  {...props}
                  type="number"
                  min={1}
                  max={1200}
                  value={draft.settings.timeout_seconds}
                  onChange={(event) => field('timeout_seconds', Number(event.target.value))}
                />
              )}
            </Field>
            <Field
              label={t('common.agent.extraParameters')}
              help={t(
                draft.settings.provider === 'chat_completions'
                  ? 'common.agent.extraHelp'
                  : 'common.agent.drivers.responsesExtraHelp',
              )}
            >
              {(props) => (
                <textarea
                  {...props}
                  rows={5}
                  spellCheck={false}
                  value={extra}
                  onChange={(event) => setExtra(event.target.value)}
                />
              )}
            </Field>
          </div>
        </Fold>
        <div className="nb-actions">
          <Button type="submit" variant="primary" disabled={unresolved}>
            {t('common.save')}
          </Button>
          {base.id ? (
            <Button type="button" onClick={() => void read.run(base.id).catch(() => undefined)}>
              {t('common.agent.loadCurrent')}
            </Button>
          ) : null}
          <Button
            type="button"
            disabled={unresolved && !base.id}
            onClick={() => {
              accept(base);
              save.reset();
              setError(null);
            }}
          >
            {t('common.agent.restoreSaved')}
          </Button>
          <Button type="button" onClick={onClose}>
            {t('common.close')}
          </Button>
          {base.id ? (
            <Button type="button" variant="danger-outline" onClick={() => setDeleting(true)}>
              {t('common.delete')}
            </Button>
          ) : null}
        </div>
        {deleting ? (
          <Note tone="warn">
            <p>{t('common.agent.drivers.deleteHelp', { name: base.name })}</p>
            <div className="nb-actions">
              <Button
                type="button"
                variant="danger"
                onClick={() =>
                  void save
                    .run({ action: 'delete', id: base.id, revision: base.settings.revision })
                    .catch(() => undefined)
                }
              >
                {t('common.delete')}
              </Button>
              <Button type="button" onClick={() => setDeleting(false)}>
                {t('common.cancel')}
              </Button>
            </div>
          </Note>
        ) : null}
        {save.error ? <AgentError error={save.error} /> : null}
        {save.refreshError ? (
          <AgentError error={save.refreshError} onRetry={() => void save.check()} />
        ) : null}
        {error ? <AgentError error={error} /> : null}
        {read.error ? <AgentError error={read.error} /> : null}
        {unresolved ? (
          <Note tone="warn">
            <p>{t('common.agent.drivers.writeUncertain')}</p>
            <Button type="button" onClick={() => void save.check()}>
              {t('common.agent.drivers.checkStatus')}
            </Button>
          </Note>
        ) : null}
        {current ? (
          <Note>
            <p>
              {t('common.agent.drivers.currentRevision', { revision: current.settings.revision })}
            </p>
            <div className="nb-actions">
              <Button
                type="button"
                onClick={() => {
                  setBase(current);
                  setCurrent(null);
                  save.reset();
                }}
              >
                {t('common.agent.keepDraft')}
              </Button>
              <Button
                type="button"
                onClick={() => {
                  accept(current);
                  save.reset();
                }}
              >
                {t('common.agent.restoreSaved')}
              </Button>
            </div>
          </Note>
        ) : null}
        {candidates.length ? (
          <Note>
            <p>{t('common.agent.drivers.creationCandidates')}</p>
            {candidates.map((candidate) => (
              <Button key={candidate.id} type="button" onClick={() => onSelect(candidate.id)}>
                {candidate.name} · {candidate.id}
              </Button>
            ))}
          </Note>
        ) : null}
        {save.isSuccess ? <Note tone="ok">{t('common.outcome.saved')}</Note> : null}
      </fieldset>
    </form>
  );
}
