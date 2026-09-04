import { useEffect, useReducer, useRef, useState, type FormEvent } from 'react';
import { useQueryClient } from '@tanstack/react-query';
import { useNavigate } from 'react-router';
import { useTranslation } from 'react-i18next';
import { LANGUAGE_STORAGE_KEY } from '@shared/i18n';
import { ApiError } from '@shared/query/http';
import { useTheme } from '@shared/theme/useTheme';
import type { Density, FontSize, Theme } from '@shared/theme/context';
import { ConfirmDialog } from '@shared/components/ConfirmDialog';
import { PageHeader } from '@shared/components/States';
import { disabledAccountLifecycleAdapter } from './adapters';
import { patchLanguage } from './api';
import { CoreErrorPanel, CoreLoading, CoreTime, MutationNotice, SafeCopyValue } from './components';
import { useCoreCopy } from './copy';
import {
  clearCoreUserSession,
  coreKeys,
  coreSessionMatchesAccount,
  currentCoreSessionLanguage,
  updateCoreSessionLanguage,
  useCoreMe,
} from './queries';
import { createOperationIdentity, isConflict, isOutcomeUnknown } from './request';
import {
  clearAccountLocalNamespace,
  clearElevatedCapabilityCookie,
  clearPendingElevation,
  downloadAccountExport,
  moveElevatedCapabilityFromCookie,
  readPendingElevation,
  writePendingElevation,
} from './sensitive';
import { initialLifecycleMachineState, lifecycleMachineReducer } from './stateMachines';
import type {
  AccountLifecycleAdapter,
  ExplicitLanguage,
  LifecycleIntent,
  UserProfile,
} from './types';

function currentExplicitLanguage(user: UserProfile, resolvedLanguage?: string): ExplicitLanguage {
  if (user.lang === 'zh' || user.lang === 'en') return user.lang;
  return resolvedLanguage?.toLowerCase().startsWith('zh') ? 'zh' : 'en';
}

export function AccountLanguageForm({ user }: { user: UserProfile }) {
  const { t } = useCoreCopy();
  const { i18n } = useTranslation();
  const queryClient = useQueryClient();
  const me = useCoreMe(user.id);
  const abortRef = useRef<AbortController | null>(null);
  const committedLanguageRef = useRef<ExplicitLanguage | null>(null);
  const attemptRef = useRef<{
    language: ExplicitLanguage;
    operation: ReturnType<typeof createOperationIdentity>;
  } | null>(null);
  const [hasAttempt, setHasAttempt] = useState(false);
  const original = currentExplicitLanguage(user, i18n.resolvedLanguage);
  const [language, setLanguage] = useState<ExplicitLanguage>(original);
  const languageMatchesAuthority = (user.lang === 'zh' || user.lang === 'en')
    && language === original;
  const [busy, setBusy] = useState(false);
  const [saved, setSaved] = useState(false);
  const [outcome, setOutcome] = useState<'conflict' | 'unknown' | 'error' | null>(null);

  const discardAttemptForBoundaryChange = () => {
    attemptRef.current = null;
    committedLanguageRef.current = null;
    setHasAttempt(false);
    setLanguage(original);
    setSaved(false);
    setOutcome(null);
  };

  const setDocumentLanguage = (confirmed: ExplicitLanguage) => {
    document.documentElement.lang = confirmed === 'zh' ? 'zh-CN' : 'en';
  };

  const restoreLanguageAfterBoundaryChange = async (fallback: ExplicitLanguage) => {
    const current = currentCoreSessionLanguage(queryClient) ?? fallback;
    await i18n.changeLanguage(current);
    setDocumentLanguage(current);
  };

  const applyConfirmedLanguage = async (
    confirmed: ExplicitLanguage,
    fallback: ExplicitLanguage,
  ): Promise<boolean> => {
    if (!coreSessionMatchesAccount(queryClient, user.id)) return false;
    await i18n.changeLanguage(confirmed);
    if (!coreSessionMatchesAccount(queryClient, user.id)) {
      await restoreLanguageAfterBoundaryChange(fallback);
      return false;
    }
    if (!updateCoreSessionLanguage(queryClient, user.id, confirmed)) {
      await restoreLanguageAfterBoundaryChange(fallback);
      return false;
    }
    try {
      window.localStorage.setItem(LANGUAGE_STORAGE_KEY, confirmed);
    } catch {
      // The confirmed account language remains authoritative even if this
      // browser blocks the optional explicit local preference.
    }
    setDocumentLanguage(confirmed);
    return true;
  };

  useEffect(
    () => () => {
      abortRef.current?.abort();
      abortRef.current = null;
    },
    [user.id],
  );

  useEffect(() => {
    let active = true;
    queueMicrotask(() => {
      if (!active) return;
      setLanguage(original);
      if (committedLanguageRef.current === original) {
        committedLanguageRef.current = null;
        return;
      }
      setSaved(false);
      setOutcome(null);
    });
    return () => {
      active = false;
    };
  }, [original, user.id]);

  const submit = async (event: FormEvent) => {
    event.preventDefault();
    if (busy || (!attemptRef.current && languageMatchesAuthority)) return;
    setBusy(true);
    setSaved(false);
    setOutcome(null);
    const attempt = attemptRef.current ?? {
      language,
      operation: createOperationIdentity(),
    };
    attemptRef.current = attempt;
    setHasAttempt(true);
    const controller = new AbortController();
    abortRef.current?.abort();
    abortRef.current = controller;
    const fallback = currentExplicitLanguage(user, i18n.resolvedLanguage);
    try {
      const envelope = await patchLanguage(attempt.language, attempt.operation, controller.signal);
      if (controller.signal.aborted || abortRef.current !== controller) return;
      if (!coreSessionMatchesAccount(queryClient, user.id)) {
        discardAttemptForBoundaryChange();
        return;
      }
      if (envelope.user.id !== user.id) {
        throw new ApiError(
          'invalid_response',
          'The server returned a different user profile.',
          200,
        );
      }
      if (currentExplicitLanguage(envelope.user, i18n.resolvedLanguage) !== attempt.language) {
        throw new ApiError(
          'invalid_response',
          'The server returned a different account language.',
          200,
        );
      }
      if (!(await applyConfirmedLanguage(attempt.language, fallback))) {
        discardAttemptForBoundaryChange();
        return;
      }
      attemptRef.current = null;
      setHasAttempt(false);
      committedLanguageRef.current = attempt.language;
      if (!coreSessionMatchesAccount(queryClient, user.id)) {
        discardAttemptForBoundaryChange();
        return;
      }
      queryClient.setQueryData(coreKeys.me(user.id), envelope);
      setSaved(true);
    } catch (error) {
      if (controller.signal.aborted || abortRef.current !== controller) return;
      if (!coreSessionMatchesAccount(queryClient, user.id)) {
        discardAttemptForBoundaryChange();
        return;
      }
      const nextOutcome = isConflict(error)
        ? 'conflict'
        : isOutcomeUnknown(error)
          ? 'unknown'
          : 'error';
      if (nextOutcome !== 'unknown') {
        attemptRef.current = null;
        setHasAttempt(false);
      }
      setOutcome(nextOutcome);
      let restored = original;
      if (nextOutcome === 'conflict' || nextOutcome === 'unknown') {
        const refreshed = await me.refetch();
        if (controller.signal.aborted || abortRef.current !== controller) return;
        if (!coreSessionMatchesAccount(queryClient, user.id)) {
          discardAttemptForBoundaryChange();
          return;
        }
        if (refreshed.data)
          restored = currentExplicitLanguage(refreshed.data.user, i18n.resolvedLanguage);
        if (
          nextOutcome === 'unknown' &&
          attemptRef.current &&
          restored === attemptRef.current.language
        ) {
          const confirmed = attemptRef.current.language;
          if (!(await applyConfirmedLanguage(confirmed, fallback))) {
            discardAttemptForBoundaryChange();
            return;
          }
          attemptRef.current = null;
          setHasAttempt(false);
          committedLanguageRef.current = confirmed;
          setSaved(true);
          setOutcome(null);
        }
      }
      setLanguage(restored);
    } finally {
      if (abortRef.current === controller) {
        abortRef.current = null;
        setBusy(false);
      }
    }
  };

  return (
    <form className="core-form" onSubmit={(event) => void submit(event)}>
      <p className="core-muted">{t('account.languageBody')}</p>
      <div className="core-field-grid">
        <label>
          <span>{t('account.language')}</span>
          <select
            disabled={busy || hasAttempt}
            value={language}
            onChange={(event) => setLanguage(event.target.value === 'zh' ? 'zh' : 'en')}
          >
            <option value="zh">{t('account.zh')}</option>
            <option value="en">{t('account.en')}</option>
          </select>
        </label>
      </div>
      {saved ? (
        <p className="core-inline-success" role="status">
          {t('account.languageSaved')}
        </p>
      ) : null}
      <MutationNotice outcome={outcome} />
      <div className="core-form-actions">
        <span />
        <button
          type="submit"
          className="btn btn-primary"
          disabled={busy || (!hasAttempt && languageMatchesAuthority)}
        >
          {busy ? t('common.working') : hasAttempt ? t('common.retrySame') : t('common.save')}
        </button>
      </div>
    </form>
  );
}

function LocalPreferences() {
  const { t } = useCoreCopy();
  const { theme, setTheme, density, setDensity, fontSize, setFontSize } = useTheme();
  const themes: Array<{ value: Theme; label: string }> = [
    { value: 'light', label: t('account.themeLight') },
    { value: 'dark', label: t('account.themeDark') },
    { value: 'system', label: t('account.themeSystem') },
  ];
  const densities: Array<{ value: Density; label: string }> = [
    { value: 'comfortable', label: t('account.densityComfortable') },
    { value: 'compact', label: t('account.densityCompact') },
  ];
  const fontSizes: Array<{ value: FontSize; label: string }> = [
    { value: 'default', label: t('account.fontDefault') },
    { value: 'large', label: t('account.fontLarge') },
  ];

  return (
    <section className="core-card core-account-preferences">
      <div className="core-card__header">
        <h2>{t('account.localTitle')}</h2>
      </div>
      <p className="core-muted">{t('account.localBody')}</p>
      <fieldset>
        <legend>{t('account.theme')}</legend>
        <div className="core-radio-group">
          {themes.map((option) => (
            <label key={option.value}>
              <input
                type="radio"
                name="account-theme"
                checked={theme === option.value}
                onChange={() => setTheme(option.value)}
              />
              <span>{option.label}</span>
            </label>
          ))}
        </div>
      </fieldset>
      <fieldset>
        <legend>{t('account.density')}</legend>
        <div className="core-radio-group">
          {densities.map((option) => (
            <label key={option.value}>
              <input
                type="radio"
                name="account-density"
                checked={density === option.value}
                onChange={() => setDensity(option.value)}
              />
              <span>{option.label}</span>
            </label>
          ))}
        </div>
      </fieldset>
      <fieldset>
        <legend>{t('account.fontSize')}</legend>
        <div className="core-radio-group">
          {fontSizes.map((option) => (
            <label key={option.value}>
              <input
                type="radio"
                name="account-font"
                checked={fontSize === option.value}
                onChange={() => setFontSize(option.value)}
              />
              <span>{option.label}</span>
            </label>
          ))}
        </div>
      </fieldset>
    </section>
  );
}

export function AccountLifecyclePanel({
  accountId,
  adapter = disabledAccountLifecycleAdapter,
}: {
  accountId: string;
  adapter?: AccountLifecycleAdapter;
}) {
  const { t } = useCoreCopy();
  const queryClient = useQueryClient();
  const navigate = useNavigate();
  const tokenRef = useRef('');
  const asyncBoundaryRef = useRef(0);
  const actionSequenceRef = useRef(0);
  const beginPendingRef = useRef(false);
  const executePendingRef = useRef(false);
  const authorityPendingRef = useRef(false);
  const [state, dispatch] = useReducer(lifecycleMachineReducer, undefined, () =>
    initialLifecycleMachineState(accountId),
  );
  const [dialog, setDialog] = useState<LifecycleIntent | null>(null);
  const [deleteWord, setDeleteWord] = useState('');
  const [deleteWordError, setDeleteWordError] = useState(false);

  const clearCapability = () => {
    tokenRef.current = '';
    clearElevatedCapabilityCookie();
    clearPendingElevation();
  };

  useEffect(() => {
    const boundary = asyncBoundaryRef.current + 1;
    asyncBoundaryRef.current = boundary;
    beginPendingRef.current = false;
    executePendingRef.current = false;
    authorityPendingRef.current = false;
    dispatch({ type: 'boundary', accountId });
    tokenRef.current = '';
    const intent = readPendingElevation(accountId);
    const token = moveElevatedCapabilityFromCookie();
    clearPendingElevation();
    if (
      intent &&
      token &&
      (intent === 'export' ? adapter.capabilities.exportV4 : adapter.capabilities.deleteAccount)
    ) {
      tokenRef.current = token;
      dispatch({ type: 'confirm', accountId, intent });
      queueMicrotask(() => {
        if (asyncBoundaryRef.current === boundary) setDialog(intent);
      });
    }
    return () => {
      if (asyncBoundaryRef.current === boundary) asyncBoundaryRef.current = boundary + 1;
      beginPendingRef.current = false;
      executePendingRef.current = false;
      authorityPendingRef.current = false;
      tokenRef.current = '';
      clearElevatedCapabilityCookie();
    };
  }, [accountId, adapter]);

  const begin = async (intent: LifecycleIntent) => {
    if (beginPendingRef.current) return;
    if (!coreSessionMatchesAccount(queryClient, accountId)) {
      clearCapability();
      dispatch({ type: 'cancel', accountId });
      return;
    }
    const boundary = asyncBoundaryRef.current;
    beginPendingRef.current = true;
    dispatch({ type: 'elevate', accountId, intent });
    try {
      const target = new URL(await adapter.beginElevation(intent, accountId));
      if (asyncBoundaryRef.current !== boundary) return;
      if (!coreSessionMatchesAccount(queryClient, accountId)) {
        clearCapability();
        dispatch({ type: 'cancel', accountId });
        return;
      }
      if (target.protocol !== 'https:') throw new Error('invalid authorization URL');
      writePendingElevation(intent, accountId);
      window.location.assign(target.toString());
    } catch {
      if (asyncBoundaryRef.current !== boundary) return;
      if (!coreSessionMatchesAccount(queryClient, accountId)) {
        clearCapability();
        dispatch({ type: 'cancel', accountId });
        return;
      }
      clearCapability();
      dispatch({
        type: 'elevation-error',
        accountId,
        intent,
        message: t('account.elevationFailed'),
      });
    } finally {
      if (asyncBoundaryRef.current === boundary) beginPendingRef.current = false;
    }
  };

  const completeDeletion = (): boolean => {
    if (!coreSessionMatchesAccount(queryClient, accountId)) return false;
    clearAccountLocalNamespace(accountId);
    clearCoreUserSession(queryClient);
    navigate('/');
    return true;
  };

  const checkAccountAuthority = async () => {
    if (
      authorityPendingRef.current ||
      state.intent !== 'delete' ||
      (state.status !== 'unknown' && state.status !== 'active')
    )
      return;
    if (!coreSessionMatchesAccount(queryClient, accountId)) {
      clearCapability();
      dispatch({ type: 'cancel', accountId });
      return;
    }
    const boundary = asyncBoundaryRef.current;
    authorityPendingRef.current = true;
    dispatch({ type: 'check-start', accountId });
    try {
      const authority = await adapter.readAccountAuthority(accountId);
      if (asyncBoundaryRef.current !== boundary) return;
      if (!coreSessionMatchesAccount(queryClient, accountId)) {
        clearCapability();
        dispatch({ type: 'cancel', accountId });
        return;
      }
      if (authority === 'deleted') {
        if (completeDeletion()) dispatch({ type: 'complete', accountId, intent: 'delete' });
        return;
      }
      dispatch({ type: 'authority-active', accountId, message: t('account.deleteStillActive') });
    } catch {
      if (asyncBoundaryRef.current !== boundary) return;
      if (!coreSessionMatchesAccount(queryClient, accountId)) {
        clearCapability();
        dispatch({ type: 'cancel', accountId });
        return;
      }
      dispatch({ type: 'authority-error', accountId, message: t('account.authorityCheckFailed') });
    } finally {
      if (asyncBoundaryRef.current === boundary) authorityPendingRef.current = false;
    }
  };

  const execute = async (intent: LifecycleIntent) => {
    if (executePendingRef.current || !tokenRef.current || dialog !== intent) return;
    if (intent === 'delete' && deleteWord !== 'DELETE') {
      setDeleteWordError(true);
      return;
    }
    if (!coreSessionMatchesAccount(queryClient, accountId)) {
      clearCapability();
      dispatch({ type: 'cancel', accountId });
      return;
    }
    const boundary = asyncBoundaryRef.current;
    executePendingRef.current = true;
    setDeleteWordError(false);
    actionSequenceRef.current += 1;
    const actionId = String(actionSequenceRef.current);
    const elevatedToken = tokenRef.current;
    dispatch({ type: 'start', accountId, intent, actionId });
    try {
      const attachment =
        intent === 'export'
          ? await adapter.exportV4({ accountId, elevatedToken })
          : await adapter
              .deleteAccount({ accountId, elevatedToken, confirmation: 'DELETE' })
              .then(() => null);
      if (asyncBoundaryRef.current !== boundary) return;
      if (!coreSessionMatchesAccount(queryClient, accountId)) {
        clearCapability();
        dispatch({ type: 'cancel', accountId });
        return;
      }
      clearCapability();
      setDialog(null);
      if (intent === 'export' && attachment) {
        downloadAccountExport(attachment);
        dispatch({ type: 'complete', accountId, intent, actionId });
      } else {
        if (completeDeletion()) dispatch({ type: 'complete', accountId, intent, actionId });
      }
    } catch (error) {
      if (asyncBoundaryRef.current !== boundary) return;
      if (!coreSessionMatchesAccount(queryClient, accountId)) {
        clearCapability();
        dispatch({ type: 'cancel', accountId });
        return;
      }
      clearCapability();
      setDialog(null);
      if (isOutcomeUnknown(error)) {
        dispatch({
          type: 'uncertain',
          accountId,
          intent,
          actionId,
          message: t(intent === 'export' ? 'account.exportUnknown' : 'account.deleteUnknown'),
        });
      } else {
        dispatch({
          type: 'error',
          accountId,
          intent,
          actionId,
          message: t('common.errorBody'),
        });
      }
    } finally {
      if (asyncBoundaryRef.current === boundary) executePendingRef.current = false;
    }
  };

  const cancel = () => {
    asyncBoundaryRef.current += 1;
    beginPendingRef.current = false;
    executePendingRef.current = false;
    authorityPendingRef.current = false;
    clearCapability();
    setDialog(null);
    setDeleteWord('');
    setDeleteWordError(false);
    dispatch({ type: 'cancel', accountId });
  };

  const busy =
    state.status === 'elevating' || state.status === 'pending' || state.status === 'checking';

  return (
    <>
      <section className="core-card">
        <div className="core-card__header">
          <h2>{t('account.exportTitle')}</h2>
        </div>
        <p>{t('account.exportBody')}</p>
        {!adapter.capabilities.exportV4 ? (
          <p className="core-inline-warning">{t('account.lifecycleUnavailable')}</p>
        ) : null}
        {state.intent === 'export' && state.status === 'error' ? (
          <p className="core-inline-error">{state.message ?? t('common.errorBody')}</p>
        ) : null}
        {state.intent === 'export' && state.status === 'unknown' ? (
          <p className="core-inline-warning">{state.message ?? t('account.exportUnknown')}</p>
        ) : null}
        <div className="core-row-actions">
          <span />
          <button
            type="button"
            className="btn btn-primary"
            disabled={!adapter.capabilities.exportV4 || busy}
            onClick={() => void begin('export')}
          >
            {state.intent === 'export' && state.status === 'elevating'
              ? t('common.working')
              : t('account.export')}
          </button>
        </div>
      </section>

      <section className="core-card core-danger-zone">
        <div className="core-card__header">
          <h2>{t('account.dangerTitle')}</h2>
        </div>
        <h3>{t('account.deleteTitle')}</h3>
        <p>{t('account.deleteBody')}</p>
        {!adapter.capabilities.deleteAccount ? (
          <p className="core-inline-warning">{t('account.lifecycleUnavailable')}</p>
        ) : null}
        {state.intent === 'delete' && state.status === 'error' ? (
          <p className="core-inline-error">{state.message ?? t('common.errorBody')}</p>
        ) : null}
        {state.intent === 'delete' &&
        (state.status === 'unknown' || state.status === 'active') ? (
          <p className="core-inline-warning">{state.message ?? t('account.deleteUnknown')}</p>
        ) : null}
        <div className="core-row-actions">
          {state.intent === 'delete' &&
          (state.status === 'unknown' ||
            state.status === 'checking' ||
            state.status === 'active') ? (
            <button
              type="button"
              className="btn btn-secondary"
              disabled={state.status === 'checking'}
              onClick={() => void checkAccountAuthority()}
            >
              {state.status === 'checking' ? t('common.working') : t('account.checkAuthority')}
            </button>
          ) : (
            <span />
          )}
          <button
            type="button"
            className="btn btn-danger"
            disabled={
              !adapter.capabilities.deleteAccount ||
              busy ||
              (state.intent === 'delete' && state.status === 'unknown')
            }
            onClick={() => void begin('delete')}
          >
            {state.intent === 'delete' && state.status === 'elevating'
              ? t('common.working')
              : t('account.delete')}
          </button>
        </div>
      </section>

      <ConfirmDialog
        open={dialog === 'export'}
        title={t('account.exportTitle')}
        description={t('account.reauthorize')}
        confirmLabel={t('account.exportConfirm')}
        busy={state.status === 'pending'}
        onCancel={cancel}
        onConfirm={() => void execute('export')}
      />
      <ConfirmDialog
        open={dialog === 'delete'}
        title={t('account.deleteTitle')}
        description={t('account.deleteBody')}
        confirmLabel={t('account.deleteConfirm')}
        danger
        busy={state.status === 'pending'}
        onCancel={cancel}
        onConfirm={() => void execute('delete')}
      >
        <label className="core-form">
          <span>{t('account.deleteWord')}</span>
          <input
            value={deleteWord}
            maxLength={6}
            autoComplete="off"
            spellCheck={false}
            onChange={(event) => {
              setDeleteWord(event.target.value);
              setDeleteWordError(false);
            }}
          />
        </label>
        {deleteWordError ? (
          <p className="core-inline-error" role="alert">
            {t('account.deleteWordError')}
          </p>
        ) : null}
      </ConfirmDialog>
    </>
  );
}

export function AccountWorkspace({
  user,
  lifecycleAdapter,
}: {
  user: UserProfile;
  lifecycleAdapter?: AccountLifecycleAdapter;
}) {
  const { t } = useCoreCopy();
  const me = useCoreMe(user.id);
  return (
    <div className="page core-page core-stack">
      <PageHeader
        icon="account"
        title={t('account.title')}
        description={t('account.description')}
      />
      <div className="core-grid core-grid--wide">
        <section className="core-card">
          <div className="core-card__header">
            <h2>{t('account.profileTitle')}</h2>
          </div>
          {me.isPending && !me.data ? (
            <CoreLoading compact />
          ) : !me.data ? (
            <CoreErrorPanel
              compact
              error={me.error ?? new Error('account profile is unavailable')}
              onRetry={() => void me.refetch()}
            />
          ) : (
            <dl className="core-detail-list">
              <div>
                <dt>{t('account.id')}</dt>
                <dd>
                  <SafeCopyValue value={me.data.user.id} label={t('account.id')} />
                </dd>
              </div>
              <div>
                <dt>{t('account.username')}</dt>
                <dd>{me.data.user.username}</dd>
              </div>
              <div>
                <dt>{t('account.nickname')}</dt>
                <dd>{me.data.user.guild_nick || t('common.notSet')}</dd>
              </div>
              <div>
                <dt>{t('account.created')}</dt>
                <dd>
                  <CoreTime value={me.data.user.created_at} />
                </dd>
              </div>
            </dl>
          )}
        </section>
        <section className="core-card">
          <div className="core-card__header">
            <h2>{t('account.languageTitle')}</h2>
          </div>
          {me.isPending && !me.data ? (
            <CoreLoading compact />
          ) : !me.data ? (
            <CoreErrorPanel
              compact
              error={me.error ?? new Error('account profile is unavailable')}
              onRetry={() => void me.refetch()}
            />
          ) : (
            <AccountLanguageForm key={me.data.user.id} user={me.data.user} />
          )}
        </section>
      </div>
      <LocalPreferences />
      <AccountLifecyclePanel accountId={user.id} adapter={lifecycleAdapter} />
    </div>
  );
}
