import { useEffect, useState, type FormEvent } from 'react';
import { useTranslation } from 'react-i18next';
import { Card, ErrorState, LoadingState, PageHeader } from '@shared/components/States';
import {
  type AdminGameConfig,
  type AdminGameConfigPatch,
  useAdminGameConfig,
  usePatchAdminGameConfig,
} from '../features/games/data';

type Draft = {
  master: boolean;
  enabled: boolean;
  worm: string;
  lure: string;
  premium: string;
  standardRTP: string;
  premiumRTP: string;
  bottle: string;
  clover: string;
  shell: string;
};

function draftFrom(snapshot: AdminGameConfig): Draft {
  return {
    master: snapshot.master_enabled,
    enabled: snapshot.fishing.enabled,
    worm: snapshot.fishing.bait_prices.worm,
    lure: snapshot.fishing.bait_prices.lure,
    premium: snapshot.fishing.bait_prices.premium,
    standardRTP: String(snapshot.fishing.rtp_percent.standard),
    premiumRTP: String(snapshot.fishing.rtp_percent.premium),
    bottle: String(snapshot.fishing.treasure_multipliers.bottle),
    clover: String(snapshot.fishing.treasure_multipliers.clover),
    shell: String(snapshot.fishing.treasure_multipliers.shell),
  };
}

function canonicalInteger(value: string, minimum: number, maximum: number): number | undefined {
  if (!/^(0|[1-9]\d*)$/.test(value)) return undefined;
  const parsed = Number(value);
  return Number.isSafeInteger(parsed) && parsed >= minimum && parsed <= maximum
    ? parsed
    : undefined;
}

function canonicalPositiveAmount(value: string): boolean {
  if (!/^[1-9]\d*$/.test(value) || value.length > 19) return false;
  try {
    return BigInt(value) <= 9_223_372_036_854_775_807n;
  } catch {
    return false;
  }
}

function buildPatch(draft: Draft, touched: ReadonlySet<keyof Draft>): AdminGameConfigPatch | undefined {
  if (touched.size === 0) return undefined;
  const patch: AdminGameConfigPatch = {};
  if (touched.has('master')) patch.master_enabled = draft.master;
  const fishing: NonNullable<AdminGameConfigPatch['fishing']> = {};
  if (touched.has('enabled')) fishing.enabled = draft.enabled;

  const prices: NonNullable<typeof fishing.bait_prices> = {};
  for (const key of ['worm', 'lure', 'premium'] as const) {
    if (!touched.has(key)) continue;
    if (!canonicalPositiveAmount(draft[key])) return undefined;
    prices[key] = draft[key];
  }
  if (Object.keys(prices).length > 0) fishing.bait_prices = prices;

  const rtp: NonNullable<typeof fishing.rtp_percent> = {};
  if (touched.has('standardRTP')) {
    const value = canonicalInteger(draft.standardRTP, 0, 100);
    if (value === undefined) return undefined;
    rtp.standard = value;
  }
  if (touched.has('premiumRTP')) {
    const value = canonicalInteger(draft.premiumRTP, 0, 100);
    if (value === undefined) return undefined;
    rtp.premium = value;
  }
  if (Object.keys(rtp).length > 0) fishing.rtp_percent = rtp;

  const multipliers: NonNullable<typeof fishing.treasure_multipliers> = {};
  for (const [draftKey, wireKey] of [
    ['bottle', 'bottle'], ['clover', 'clover'], ['shell', 'shell'],
  ] as const) {
    if (!touched.has(draftKey)) continue;
    const value = canonicalInteger(draft[draftKey], 1, 1_000);
    if (value === undefined) return undefined;
    multipliers[wireKey] = value;
  }
  if (Object.keys(multipliers).length > 0) fishing.treasure_multipliers = multipliers;
  if (Object.keys(fishing).length > 0) patch.fishing = fishing;
  return patch;
}

function GamesForm({ snapshot }: { snapshot: AdminGameConfig }) {
  const { t } = useTranslation();
  const mutation = usePatchAdminGameConfig();
  const [draft, setDraft] = useState(() => draftFrom(snapshot));
  const [touched, setTouched] = useState<Set<keyof Draft>>(() => new Set());
  const [validation, setValidation] = useState('');

  useEffect(() => {
    // Every successful PATCH returns the complete authoritative snapshot.
    // eslint-disable-next-line react-hooks/set-state-in-effect
    setDraft(draftFrom(snapshot));
    setTouched(new Set());
  }, [snapshot]);

  const change = <K extends keyof Draft>(key: K, value: Draft[K]) => {
    mutation.reset();
    setDraft((current) => ({ ...current, [key]: value }));
    setTouched((current) => new Set(current).add(key));
    setValidation('');
  };

  const submit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    mutation.reset();
    const patch = buildPatch(draft, touched);
    if (!patch) {
      setValidation(t('admin.games.invalid'));
      return;
    }
    mutation.mutate(patch);
  };

  const numberField = (key: Exclude<keyof Draft, 'master' | 'enabled'>, label: string) => (
    <label>
      <span>{label}</span>
      <input
        value={draft[key]}
        inputMode="numeric"
        onChange={(event) => change(key, event.target.value)}
        aria-label={label}
        disabled={mutation.isPending}
      />
    </label>
  );

  return (
    <form onSubmit={submit} noValidate>
      <div className="form-grid">
        <label className="checkbox-label">
          <input type="checkbox" checked={draft.master} onChange={(event) => change('master', event.target.checked)} disabled={mutation.isPending} />
          <span>{t('admin.games.masterEnabled')}</span>
        </label>
        <label className="checkbox-label">
          <input type="checkbox" checked={draft.enabled} onChange={(event) => change('enabled', event.target.checked)} disabled={mutation.isPending} />
          <span>{t('admin.games.fishingEnabled')}</span>
        </label>
      </div>
      <fieldset>
        <legend>{t('admin.games.prices')}</legend>
        <p className="muted">{t('admin.games.priceHint')}</p>
        <div className="form-grid">
          {numberField('worm', t('admin.games.worm'))}
          {numberField('lure', t('admin.games.lure'))}
          {numberField('premium', t('admin.games.premium'))}
        </div>
      </fieldset>
      <fieldset>
        <legend>{t('admin.games.rtp')}</legend>
        <div className="form-grid">
          {numberField('standardRTP', t('admin.games.standardRTP'))}
          {numberField('premiumRTP', t('admin.games.premiumRTP'))}
        </div>
      </fieldset>
      <fieldset>
        <legend>{t('admin.games.multipliers')}</legend>
        <div className="form-grid">
          {numberField('bottle', t('admin.games.bottle'))}
          {numberField('clover', t('admin.games.clover'))}
          {numberField('shell', t('admin.games.shell'))}
        </div>
      </fieldset>
      {validation ? <p className="field-error" role="alert">{validation}</p> : null}
      {mutation.error ? <ErrorState error={mutation.error} /> : null}
      {mutation.isSuccess ? <p className="inline-success" role="status">{t('admin.games.saved')}</p> : null}
      <div className="form-actions">
        <button className="btn btn-primary" type="submit" disabled={mutation.isPending}>
          {mutation.isPending ? t('common.working') : t('admin.games.save')}
        </button>
      </div>
    </form>
  );
}

// Central route and navigation wiring is intentionally deferred to the final
// application integration; this standalone page remains directly testable.
export function GamesPage() {
  const { t } = useTranslation();
  const config = useAdminGameConfig();
  return (
    <div className="page">
      <PageHeader eyebrow={t('app.name')} title={t('admin.games.title')} description={t('admin.games.description')} />
      <Card>
        {config.isPending ? <LoadingState /> : config.error ? (
          <ErrorState error={config.error} onRetry={() => void config.refetch()} />
        ) : <GamesForm snapshot={config.data} />}
      </Card>
    </div>
  );
}
