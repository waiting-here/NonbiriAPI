import { useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';
import { Button } from './ui/Button';
import {
  Segmented,
  Field,
  Note,
  OutcomeNote,
  SaveBar,
  Toggle,
  type OutcomePresentation,
} from './ui';
import { EmptyState, ErrorState, LoadingState, SectionHead } from './States';
import { managementRoot, type ManagementRole } from '@shared/operations/managedUsers';
import { useRetainedOperation } from '@shared/operations/useRetainedOperation';
import {
  defaultStewardPermissions,
  DONATION_SCOPES,
  getStewardPermissions,
  saveStewardPermissions,
  STEWARD_PERMISSION_DEFAULTS,
  STEWARD_LIMIT_MAXIMUMS,
  stewardPermissionKeys,
  stewardLimitKeys,
  type StewardPermissions,
  type StewardLimit,
  type StewardPermission,
} from '@shared/operations/stewardPermissions';

import {
  permissionGroups,
  permissionPairMode,
  applyPermissionPair,
  type PermissionMode,
} from '@shared/operations/stewardPermissionGroups';
import { permissionPresentationCopy } from './permissionPresentationCopy';
import './stewardPermissionEditor.css';

const copyKey = (key: string) => key.replaceAll('.', '_');
const isMoney = (key: StewardLimit) => key.endsWith('_milli');

function displayLimit(key: StewardLimit, value: string) {
  if (!isMoney(key)) return value;
  const amount = BigInt(value);
  const fraction = (amount % 1000n).toString().padStart(3, '0').replace(/0+$/, '');
  return `${amount / 1000n}${fraction ? `.${fraction}` : ''}`;
}

export function parseStewardLimitInput(key: StewardLimit, value: string): string | null {
  if (value.length > 32) return null;
  if (!(isMoney(key) ? /^\d+(?:\.\d{1,3})?$/ : /^\d+$/).test(value)) return null;
  const [whole, fraction = ''] = value.split('.');
  const amount = isMoney(key)
    ? BigInt(whole) * 1000n + BigInt(fraction.padEnd(3, '0'))
    : BigInt(whole);
  return amount <= BigInt(STEWARD_LIMIT_MAXIMUMS[key]) ? amount.toString() : null;
}

export function StewardPermissionEditor({
  role,
  account,
  userId,
}: {
  role: ManagementRole;
  account: string;
  userId: string;
}) {
  const [saved, setSaved] = useState(false);
  const query = useQuery({
    queryKey: [...managementRoot(role), 'steward-permissions', account, userId],
    queryFn: ({ signal }) => getStewardPermissions(role, userId, signal),
    staleTime: Infinity,
    refetchOnMount: 'always',
    refetchOnWindowFocus: false,
    refetchOnReconnect: false,
  });
  if (query.isPending) return <LoadingState />;
  if (!query.data) return <ErrorState error={query.error} onRetry={() => void query.refetch()} />;
  const refresh = async () => {
    const result = await query.refetch();
    if (result.error) throw result.error;
  };
  return (
    <>
      {saved ? <OutcomeNote outcome={{ kind: 'saved' }} /> : null}
      <PermissionForm
        key={`${userId}:${query.data.revision}`}
        role={role}
        userId={userId}
        policy={query.data}
        refresh={refresh}
        onSaved={() => setSaved(true)}
        onEdit={() => setSaved(false)}
      />
    </>
  );
}

function PermissionForm({
  role,
  userId,
  policy,
  refresh,
  onSaved,
  onEdit,
}: {
  role: ManagementRole;
  userId: string;
  policy: StewardPermissions;
  refresh: () => Promise<unknown>;
  onSaved: () => void;
  onEdit: () => void;
}) {
  const { t } = useTranslation();
  const copy = permissionPresentationCopy(t);
  const [permissions, setPermissions] = useState(policy.permissions);
  const [donationScope, setDonationScope] = useState(policy.donation_scope);
  const inputsFor = (value: StewardPermissions) =>
    Object.fromEntries(
      stewardLimitKeys.map((key) => [key, displayLimit(key, value.limits[key])]),
    ) as Record<StewardLimit, string>;
  const [inputs, setInputs] = useState(() => inputsFor(policy));
  const [filter, setFilter] = useState('');
  const [category, setCategory] = useState('all');
  const limits = Object.fromEntries(
    stewardLimitKeys.map((key) => [key, parseStewardLimitInput(key, inputs[key])]),
  ) as Record<StewardLimit, string | null>;
  const valid = stewardLimitKeys.every((key) => limits[key] !== null);
  const dirty =
    Number(donationScope !== policy.donation_scope) +
    stewardPermissionKeys.filter((key) => permissions[key] !== policy.permissions[key]).length +
    stewardLimitKeys.filter((key) => limits[key] !== policy.limits[key]).length;
  const mutation = useRetainedOperation(
    (next: StewardPermissions, key, context) =>
      saveStewardPermissions(role, userId, next, key, context.signal),
    async (_next, error) => {
      if (!error) {
        await refresh();
        onSaved();
      }
    },
    managementRoot(role),
  );
  const locked =
    mutation.isPending || ['unknown', 'conflict', 'refresh-failed'].includes(mutation.outcome);
  const recheck = () => {
    if (mutation.outcome === 'unknown' && mutation.variables) mutation.mutate(mutation.variables);
    else if (mutation.outcome === 'refresh-failed') void mutation.refresh();
    else
      void refresh()
        .then(() => mutation.reset())
        .catch(() => undefined);
  };
  const outcome: OutcomePresentation =
    mutation.outcome === 'unknown'
      ? { kind: 'unknown', recheck }
      : mutation.outcome === 'conflict'
        ? { kind: 'conflict', reload: recheck }
        : mutation.outcome === 'refresh-failed'
          ? { kind: 'savedRefreshFailed', recheck }
          : { kind: 'idle' };
  const apply = (level: number) => {
    onEdit();
    const next = defaultStewardPermissions(level);
    setPermissions(next.permissions);
    setDonationScope(next.donation_scope);
    setInputs(inputsFor(next));
  };
  const matches = (value: string) =>
    value.toLocaleLowerCase().includes(filter.trim().toLocaleLowerCase());
  const groups = permissionGroups
    .filter((group) => category === 'all' || category === group.id)
    .map((group) => ({
      ...group,
      visible: group.keys.filter(
        (key) =>
          matches(t(`management.permissions.labels.${copyKey(key)}`)) ||
          matches(copy.groups[group.id].title) ||
          matches(copy.groups[group.id].help),
      ),
    }))
    .filter((group) => group.visible.length > 0);
  const toggle = (key: StewardPermission) => (
    <Toggle
      key={key}
      label={t(`management.permissions.labels.${copyKey(key)}`)}
      description={t(
        STEWARD_PERMISSION_DEFAULTS[key]
          ? 'management.permissions.defaultAllowed'
          : 'management.permissions.defaultDenied',
      )}
      checked={permissions[key]}
      disabled={locked}
      onChange={(checked) => {
        onEdit();
        setPermissions((current) => ({ ...current, [key]: checked }));
      }}
    />
  );
  const showLimits =
    (category === 'all' || category === 'limits') &&
    (!filter.trim() ||
      matches(t('management.permissions.limitsTitle')) ||
      stewardLimitKeys.some((key) => matches(t(`management.permissions.limits.${copyKey(key)}`))));
  return (
    <div className="nb-stack">
      <SectionHead
        title={t('management.permissions.title')}
        description={t('management.permissions.description')}
      />
      <Note>{t('management.permissions.hardLimit')}</Note>
      <p className="nb-muted">{t('management.permissions.combinedActions')}</p>
      <div className="nb-actions">
        <Button type="button" disabled={locked} onClick={() => apply(5)}>
          {t('management.permissions.defaults')}
        </Button>
        <Button type="button" disabled={locked} onClick={() => apply(6)}>
          {t('management.permissions.all')}
        </Button>
        <Button type="button" disabled={locked} onClick={() => apply(0)}>
          {t('management.permissions.none')}
        </Button>
      </div>
      <Field label={t('management.permissions.search')}>
        {(field) => (
          <input
            {...field}
            value={filter}
            placeholder={t('management.permissions.searchPlaceholder')}
            onChange={(event) => {
              setFilter(event.target.value);
              setCategory('all');
            }}
          />
        )}
      </Field>
      <nav className="permission-categories" aria-label={t('management.permissions.categories')}>
        {[{ id: 'all' }, ...permissionGroups, { id: 'limits' }].map((group) => (
          <Button
            key={group.id}
            type="button"
            aria-pressed={category === group.id}
            onClick={() => {
              setCategory(group.id);
              setFilter('');
            }}
          >
            {group.id === 'all'
              ? t('management.permissions.allCategories')
              : group.id === 'limits'
                ? t('management.permissions.limitsTitle')
                : copy.groups[group.id].title}
            {'keys' in group
              ? ` · ${group.keys.filter((key) => permissions[key]).length}/${group.keys.length}`
              : ''}
          </Button>
        ))}
      </nav>
      <Note>{t('management.permissions.fixedExemptions')}</Note>
      {groups.length === 0 && !showLimits ? (
        <EmptyState
          title={t('management.permissions.noMatches')}
          body={t('common.noResultsBody')}
        />
      ) : null}
      {groups.map((group) => (
        <section
          key={group.id}
          className="permission-group"
          aria-label={copy.groups[group.id].title}
        >
          <SectionHead
            title={copy.groups[group.id].title}
            description={copy.groups[group.id].help}
          />
          {group.id === 'donations' ? (
            <div className="permission-scope">
              <Field
                label={t('management.permissions.scope.title')}
                help={t('management.permissions.scope.help')}
              >
                {(field) => (
                  <>
                    <strong>{t(`management.permissions.scope.${donationScope}.label`)}</strong>
                    <input
                      {...field}
                      type="range"
                      min="0"
                      max="3"
                      step="1"
                      value={DONATION_SCOPES.indexOf(donationScope)}
                      aria-valuetext={t(`management.permissions.scope.${donationScope}.label`)}
                      disabled={locked}
                      onChange={(event) => {
                        onEdit();
                        setDonationScope(DONATION_SCOPES[Number(event.target.value)]);
                      }}
                    />
                  </>
                )}
              </Field>
              <dl className="nb-stack">
                {DONATION_SCOPES.map((scope) => (
                  <div key={scope}>
                    <dt>
                      <strong>{t(`management.permissions.scope.${scope}.label`)}</strong>
                    </dt>
                    <dd>{t(`management.permissions.scope.${scope}.description`)}</dd>
                  </div>
                ))}
              </dl>
            </div>
          ) : null}
          <div className="permission-grid">
            {(group.pairs ?? [])
              .filter(
                (pair) => group.visible.includes(pair.read) || group.visible.includes(pair.manage),
              )
              .map((pair) => {
                const mode = permissionPairMode(permissions, pair);
                return (
                  <div key={pair.id} className="permission-pair">
                    <strong>{copy.pairs[pair.id]}</strong>
                    <Segmented<PermissionMode>
                      label={copy.pairs[pair.id]}
                      value={mode}
                      disabled={locked}
                      options={(
                        [
                          'off',
                          'read',
                          'manage',
                          ...(mode === 'custom' ? ['custom'] : []),
                        ] as PermissionMode[]
                      ).map((value) => ({
                        value,
                        label: copy.modes[value],
                      }))}
                      onChange={(next) => {
                        onEdit();
                        setPermissions((current) => applyPermissionPair(current, pair, next));
                      }}
                    />
                    <p className="nb-muted">
                      {t('management.permissions.manageIncludes', {
                        action: t(`management.permissions.labels.${copyKey(pair.manage)}`),
                      })}
                    </p>
                    <details open={filter.trim() ? true : undefined}>
                      <summary>{t('management.permissions.individual')}</summary>
                      <div className="nb-stack">
                        {toggle(pair.read)}
                        {toggle(pair.manage)}
                      </div>
                    </details>
                  </div>
                );
              })}
            {group.visible
              .filter(
                (key) => !group.pairs?.some((pair) => pair.read === key || pair.manage === key),
              )
              .map(toggle)}
          </div>
        </section>
      ))}
      {showLimits ? (
        <section className="permission-group">
          <SectionHead
            title={t('management.permissions.limitsTitle')}
            description={t('management.permissions.limitsHelp')}
          />
          <div className="nb-stack">
            {stewardLimitKeys.map((key) => (
              <Field
                key={key}
                label={t(`management.permissions.limits.${copyKey(key)}`)}
                error={limits[key] === null ? t('management.permissions.invalidLimit') : undefined}
                help={t('management.permissions.maximum', {
                  value: displayLimit(key, STEWARD_LIMIT_MAXIMUMS[key]),
                })}
              >
                {(field) => (
                  <>
                    <input
                      {...field}
                      type="number"
                      min="0"
                      max={displayLimit(key, STEWARD_LIMIT_MAXIMUMS[key])}
                      step={isMoney(key) ? '0.001' : '1'}
                      value={inputs[key]}
                      disabled={locked}
                      onChange={(event) => {
                        onEdit();
                        setInputs((current) => ({ ...current, [key]: event.target.value }));
                      }}
                    />
                    {!isMoney(key) ? (
                      <input
                        type="range"
                        min="0"
                        max={STEWARD_LIMIT_MAXIMUMS[key]}
                        step="1"
                        aria-label={t(`management.permissions.limits.${copyKey(key)}`)}
                        value={limits[key] ?? '0'}
                        disabled={locked}
                        onChange={(event) => {
                          onEdit();
                          setInputs((current) => ({ ...current, [key]: event.target.value }));
                        }}
                      />
                    ) : null}
                  </>
                )}
              </Field>
            ))}
          </div>
        </section>
      ) : null}
      {mutation.error && outcome.kind === 'idle' ? <ErrorState error={mutation.error} /> : null}
      <OutcomeNote outcome={outcome} busy={mutation.isPending} />
      <SaveBar
        dirtyCount={dirty}
        busy={locked}
        saveDisabled={!valid}
        saveLabel={t('management.permissions.save')}
        discardLabel={t('management.permissions.discard')}
        dirtyLabel={(count) => t('management.permissions.dirty', { count })}
        onDiscard={() => {
          setPermissions(policy.permissions);
          setDonationScope(policy.donation_scope);
          setInputs(inputsFor(policy));
        }}
        onSave={() => {
          if (valid)
            void mutation
              .mutateAsync({
                revision: policy.revision,
                donation_scope: donationScope,
                permissions,
                limits: limits as StewardPermissions['limits'],
              })
              .catch(() => undefined);
        }}
      />
    </div>
  );
}
