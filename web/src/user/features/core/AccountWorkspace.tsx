import { OutcomeNote, Segmented } from '@shared/components/ui';
import { useEffect, useRef, useState, type ReactNode } from 'react';
import { useQueryClient } from '@tanstack/react-query';
import { Link, useNavigate } from 'react-router';
import { useTranslation } from 'react-i18next';
import { LANGUAGE_STORAGE_KEY } from '@shared/i18n';
import { ApiError } from '@shared/query/http';
import { useTheme } from '@shared/theme/useTheme';
import type { Density, FontSize, Theme } from '@shared/theme/context';
import { ConfirmDialog } from '@shared/components/ConfirmDialog';
import { PageHeader } from '@shared/components/States';
import { disabledAccountLifecycleAdapter } from './adapters';
import { patchLanguage } from './api';
import { CoreErrorPanel, CoreLoading, CoreTime, SafeCopyValue } from './components';
import { useCoreCopy } from './copy';
import './account-preferences.css';
import { MusicQualityPreference } from './MusicQualityPreference';
import {
  clearCoreUserSession,
  coreKeys,
  coreSessionMatchesAccount,
  currentCoreSessionLanguage,
  updateCoreSessionLanguage,
  useCoreMe,
} from './queries';
import { isOutcomeUnknown } from './request';
import {
  useRetainedOperation,
  type OperationContext,
} from '@shared/operations/useRetainedOperation';
import { useOperation } from '@shared/operations/useOperation';
import { useElevationReturn } from '@shared/operations/useElevationReturn';
import {
  clearAccountLocalNamespace,
  clearElevatedCapabilityCookie,
  clearPendingElevation,
  downloadAccountExport,
  writePendingElevation,
} from './sensitive';
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
  const original = currentExplicitLanguage(user, i18n.resolvedLanguage);
  const [language, setLanguage] = useState<ExplicitLanguage | ''>(user.lang === '' ? '' : original);
  const committed = useRef<ExplicitLanguage | null>(null);

  const applyLanguage = async (confirmed: ExplicitLanguage, context: OperationContext) => {
    context.assertCurrent();
    await i18n.changeLanguage(confirmed);
    if (!context.isCurrent()) {
      const current = currentCoreSessionLanguage(queryClient) ?? original;
      await i18n.changeLanguage(current);
      document.documentElement.lang = current === 'zh' ? 'zh-CN' : 'en';
      context.assertCurrent();
    }
    context.commit(() => {
      updateCoreSessionLanguage(queryClient, user.id, confirmed);
      try {
        window.localStorage.setItem(LANGUAGE_STORAGE_KEY, confirmed);
      } catch {
        // Account language remains authoritative when optional browser storage is blocked.
      }
      document.documentElement.lang = confirmed === 'zh' ? 'zh-CN' : 'en';
      committed.current = confirmed;
      setLanguage(confirmed);
    });
  };
  const operation = useRetainedOperation<ExplicitLanguage, void>(
    async (intent, key, context) => {
      const envelope = await patchLanguage(
        intent,
        { idempotencyKey: key, actionId: key },
        context.signal,
      );
      if (envelope.user.id !== user.id || envelope.user.lang !== intent)
        throw new ApiError(
          'invalid_response',
          'The server returned a different account language.',
          200,
        );
      await applyLanguage(intent, context);
      context.commit(() => queryClient.setQueryData(coreKeys.me(user.id), envelope));
    },
    async (intent, error, context) => {
      if (!error) return;
      const refreshed = await me.refetch();
      context.assertCurrent();
      const restored = refreshed.data
        ? currentExplicitLanguage(refreshed.data.user, i18n.resolvedLanguage)
        : original;
      if (isOutcomeUnknown(error) && refreshed.data && restored === intent) {
        await applyLanguage(intent, context);
        return { operationConfirmed: true };
      }
      context.commit(() => setLanguage(restored));
    },
    ['user', 'core'],
    { clearSecrets: () => setLanguage(user.lang === '' ? '' : original) },
  );

  const resetOperation = operation.reset;
  useEffect(() => {
    let active = true;
    queueMicrotask(() => {
      if (!active) return;
      setLanguage(user.lang === '' ? '' : original);
      if (committed.current === original) committed.current = null;
      else resetOperation();
    });
    return () => {
      active = false;
    };
  }, [original, user.id, user.lang, resetOperation]);

  const pendingIntent = operation.outcome === 'unknown' ? operation.variables : undefined;
  const outcome =
    operation.outcome === 'unknown'
      ? 'unknown'
      : operation.outcome === 'conflict'
        ? 'conflict'
        : operation.outcome === 'failed'
          ? 'error'
          : null;
  return (
    <div className="account-preference-language">
      <div className="account-preference-row">
        <div>
          <h3>{t('account.language')}</h3>
          <p className="core-muted">{t('account.languageBody')}</p>
        </div>
        <Segmented
          label={t('account.language')}
          value={language as ExplicitLanguage}
          options={[
            { value: 'zh', label: t('account.zh') },
            { value: 'en', label: t('account.en') },
          ]}
          disabled={operation.isPending || Boolean(pendingIntent)}
          onChange={(next) => {
            if (next === language && user.lang === next) return;
            setLanguage(next);
            operation.reset();
            operation.mutate(next);
          }}
        />
      </div>
      <OutcomeNote
        busy={operation.isPending}
        outcome={
          operation.outcome === 'refresh-failed'
            ? { kind: 'savedRefreshFailed', recheck: () => void operation.refresh() }
            : operation.isSuccess
              ? { kind: 'saved' }
              : outcome === 'conflict'
                ? { kind: 'conflict', inputPreserved: false, reload: () => void operation.check() }
                : outcome === 'unknown'
                  ? { kind: 'unknown', recheck: () => void operation.check() }
                  : outcome === 'error'
                    ? { kind: 'failed', message: t('common.errorBody') }
                    : { kind: 'idle' }
        }
      />
      {pendingIntent ? (
        <button
          type="button"
          className="nb-btn nb-btn--secondary"
          disabled={operation.isPending}
          onClick={() => operation.mutate(pendingIntent)}
        >
          {t('common.retrySame')}
        </button>
      ) : null}
    </div>
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
    <div className="account-local-preferences">
      {[
        {
          label: t('account.theme'),
          hint: t('accountPresentation.themeHint'),
          value: theme,
          options: themes,
          change: (value: string) => setTheme(value as Theme),
        },
        {
          label: t('account.density'),
          hint: t('accountPresentation.densityHint'),
          value: density,
          options: densities,
          change: (value: string) => setDensity(value as Density),
        },
        {
          label: t('account.fontSize'),
          hint: t('accountPresentation.fontHint'),
          value: fontSize,
          options: fontSizes,
          change: (value: string) => setFontSize(value as FontSize),
        },
      ].map((preference) => (
        <div className="account-preference-row" key={preference.label}>
          <div>
            <h3>{preference.label}</h3>
            <p className="core-muted">{preference.hint}</p>
          </div>
          <Segmented
            label={preference.label}
            value={preference.value}
            options={preference.options}
            onChange={preference.change}
          />
        </div>
      ))}
      <MusicQualityPreference />
    </div>
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
  const [intent, setIntent] = useState<LifecycleIntent | null>(null);
  const [dialog, setDialog] = useState(false);
  const clearCapability = () => {
    tokenRef.current = '';
    clearElevatedCapabilityCookie();
    clearPendingElevation();
  };
  const completeDeletion = () => {
    clearAccountLocalNamespace(accountId);
    clearCoreUserSession(queryClient);
    navigate('/');
  };
  const execute = useOperation<LifecycleIntent | 'confirm-delete', string>({
    authorityRoot: ['user', 'core'],
    clearSecrets: () => {
      tokenRef.current = '';
      setDialog(false);
      setIntent(null);
    },
    execute: async (action, token, _key, context) => {
      if (!token || !coreSessionMatchesAccount(queryClient, accountId))
        throw new ApiError('elevated_required', 'Verify the account identity again.', 403);
      if (action === 'confirm-delete') {
        context.commit(() => {
          tokenRef.current = token;
          setIntent('delete');
          setDialog(true);
        });
        return;
      }
      tokenRef.current = '';
      if (action === 'export') {
        const attachment = await adapter.exportAccount({
          accountId,
          elevatedToken: token,
          signal: context.signal,
        });
        context.commit(() => downloadAccountExport(attachment));
      } else {
        await adapter.deleteAccount({
          accountId,
          elevatedToken: token,
          confirmation: 'DELETE',
          signal: context.signal,
        });
        context.commit(completeDeletion);
      }
    },
    reconcile: () => undefined,
  });
  const elevation = useOperation<LifecycleIntent>({
    authorityRoot: ['user', 'core'],
    execute: async (action, _secret, _key, context) => {
      if (!coreSessionMatchesAccount(queryClient, accountId))
        throw new ApiError('invalid_request', 'The account session changed.', 400);
      const target = new URL(await adapter.beginElevation(action, accountId, context.signal));
      if (target.protocol !== 'https:') throw new Error('invalid authorization URL');
      context.commit(() => {
        writePendingElevation(action, accountId);
        window.location.assign(target.toString());
      });
    },
    reconcile: () => undefined,
  });
  const authority = useOperation<null, never, 'active' | 'deleted'>({
    authorityRoot: ['user', 'core'],
    execute: async (_intent, _secret, _key, context) => {
      const result = await adapter.readAccountAuthority(accountId, context.signal);
      if (result === 'deleted') context.commit(completeDeletion);
      return result;
    },
    reconcile: () => undefined,
  });
  useEffect(() => {
    return () => {
      tokenRef.current = '';
    };
  }, [accountId]);
  useElevationReturn(accountId, (returnedIntent, token, context) => {
    if (
      !(returnedIntent === 'export'
        ? adapter.capabilities.exportAccount
        : adapter.capabilities.deleteAccount)
    )
      return;
    context.commit(() => {
      setIntent(returnedIntent);
      void execute
        .run(returnedIntent === 'delete' ? 'confirm-delete' : 'export', token)
        .catch(() => undefined);
    });
  });
  const begin = (action: LifecycleIntent) => {
    setIntent(action);
    execute.reset();
    authority.reset();
    void elevation.run(action).catch(() => undefined);
  };
  const cancel = () => {
    execute.cancel();
    elevation.cancel();
    authority.cancel();
    clearCapability();
    setDialog(false);
  };
  const busy = execute.isPending || elevation.isPending || authority.isPending;
  const unknown = execute.outcome === 'unknown';
  const failed = execute.outcome === 'failed' || execute.outcome === 'conflict';
  return (
    <>
      <section className="core-card">
        <div className="core-card__header">
          <h2>{t('account.exportTitle')}</h2>
        </div>
        <p>{t('accountPresentation.exportBody')}</p>
        {!adapter.capabilities.exportAccount ? (
          <p className="core-inline-warning">{t('account.lifecycleUnavailable')}</p>
        ) : null}
        {intent === 'export' && (failed || elevation.isError) ? (
          <p className="core-inline-error">
            {t(elevation.isError ? 'account.elevationFailed' : 'common.errorBody')}
          </p>
        ) : null}
        {intent === 'export' && unknown ? (
          <p className="core-inline-warning">{t('account.exportUnknown')}</p>
        ) : null}
        <div className="core-row-actions">
          {execute.isPending && intent === 'export' ? (
            <button type="button" className="nb-btn nb-btn--secondary" onClick={cancel}>
              {t('common.cancel')}
            </button>
          ) : (
            <span />
          )}
          <button
            type="button"
            className="nb-btn nb-btn--secondary"
            disabled={!adapter.capabilities.exportAccount || busy}
            onClick={() => begin('export')}
          >
            {busy && intent === 'export'
              ? t('common.working')
              : t('accountPresentation.exportAction')}
          </button>
        </div>
      </section>
      <section className="core-card nb-panel--danger account-delete-panel">
        <div className="core-card__header">
          <h2>{t('account.deleteTitle')}</h2>
        </div>
        <p>{t('account.deleteBody')}</p>
        {!adapter.capabilities.deleteAccount ? (
          <p className="core-inline-warning">{t('account.lifecycleUnavailable')}</p>
        ) : null}
        {intent === 'delete' && (failed || elevation.isError) ? (
          <p className="core-inline-error">
            {t(elevation.isError ? 'account.elevationFailed' : 'common.errorBody')}
          </p>
        ) : null}
        {intent === 'delete' && unknown ? (
          <p className="core-inline-warning">{t('account.deleteUnknown')}</p>
        ) : null}
        {authority.data === 'active' ? (
          <p className="core-inline-warning">{t('account.deleteStillActive')}</p>
        ) : null}
        {authority.isError ? (
          <p className="core-inline-error">{t('account.authorityCheckFailed')}</p>
        ) : null}
        <div className="core-row-actions">
          {intent === 'delete' && unknown ? (
            <button
              type="button"
              className="nb-btn nb-btn--secondary"
              disabled={busy}
              onClick={() => void authority.run(null).catch(() => undefined)}
            >
              {t('account.checkAuthority')}
            </button>
          ) : (
            <span />
          )}
          <button
            type="button"
            className="nb-btn btn-danger-outline"
            disabled={
              !adapter.capabilities.deleteAccount ||
              busy ||
              (intent === 'delete' && unknown && authority.data !== 'active')
            }
            onClick={() => begin('delete')}
          >
            {busy && intent === 'delete'
              ? t('common.working')
              : t('accountPresentation.deleteAction')}
          </button>
        </div>
      </section>
      <ConfirmDialog
        open={dialog}
        title={t('account.deleteTitle')}
        description={t('account.deleteBody')}
        confirmLabel={t('account.deleteConfirm')}
        danger
        busy={execute.isPending}
        onCancel={cancel}
        onConfirm={() => {
          setDialog(false);
          void execute.run('delete', tokenRef.current).catch(() => undefined);
        }}
      />
    </>
  );
}

export function AccountWorkspace({
  user,
  lifecycleAdapter,
  activity,
}: {
  user: UserProfile;
  lifecycleAdapter?: AccountLifecycleAdapter;
  activity?: ReactNode;
}) {
  const { t } = useCoreCopy();
  const me = useCoreMe(user.id);
  return (
    <div className="page core-page core-stack account-workspace">
      <PageHeader
        icon="account"
        title={t('account.title')}
        description={t('account.description')}
        actions={
          <Link className="nb-btn nb-btn--secondary" to="/credits">
            {t('home.creditHistory')}
          </Link>
        }
      />
      <div className="account-profile">
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
            <dl className="nb-facts nb-facts--inline">
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
      </div>
      <section className="core-card core-account-preferences">
        <div className="core-card__header">
          <h2>{t('accountPresentation.preferences')}</h2>
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
        <LocalPreferences />
      </section>
      {activity}
      <AccountLifecyclePanel accountId={user.id} adapter={lifecycleAdapter} />
    </div>
  );
}
