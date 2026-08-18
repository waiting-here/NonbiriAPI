import { useState, type FormEvent } from 'react';
import { useQueryClient } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';
import {
  Card,
  EmptyState,
  ErrorState,
  LoadingState,
  PageHeader,
} from '@shared/components/States';
import { apiFetch } from '@shared/query/http';
import { adminKeys, type SiteConfigValue, useSiteConfig } from '../data';

function ConfigEditor({ name, initialValue }: { name: string; initialValue: SiteConfigValue }) {
  const { t } = useTranslation();
  const queryClient = useQueryClient();
  const [value, setValue] = useState(initialValue === null ? '' : String(initialValue));
  const [error, setError] = useState('');
  const [saved, setSaved] = useState(false);
  const [busy, setBusy] = useState(false);

  const typeLabel =
    typeof initialValue === 'boolean'
      ? t('admin.settings.booleanValue')
      : typeof initialValue === 'number'
        ? t('admin.settings.numberValue')
        : t('admin.settings.textValue');
  const title = t(`admin.settings.configKeys.${name}.title`, { defaultValue: name });
  const description = t(`admin.settings.configKeys.${name}.description`, { defaultValue: '' });

  const submit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    setError('');
    setSaved(false);
    let nextValue: SiteConfigValue;
    if (typeof initialValue === 'boolean') {
      nextValue = value === 'true';
    } else if (typeof initialValue === 'number') {
      if (!value.trim() || !Number.isFinite(Number(value))) {
        setError(t('admin.settings.invalidNumber'));
        return;
      }
      nextValue = Number(value);
    } else if (initialValue === null) {
      nextValue = value.trim() ? value.trim() : null;
    } else {
      nextValue = value;
    }

    setBusy(true);
    try {
      await apiFetch<unknown>(`/admin/api/site-config/${encodeURIComponent(name)}`, {
        method: 'PATCH',
        json: { value: nextValue },
      });
      await queryClient.invalidateQueries({ queryKey: adminKeys.siteConfig });
      setSaved(true);
    } catch (requestError) {
      setError(requestError instanceof Error ? requestError.message : t('common.errorBody'));
    } finally {
      setBusy(false);
    }
  };

  return (
    <form className="config-row" onSubmit={submit} noValidate>
      <div className="config-key-info">
        <strong>{title}</strong>
        {description ? <span className="table-note">{description}</span> : null}
        <span className="mono table-note">{name} · {typeLabel}</span>
      </div>
      <div className="config-control">
        {typeof initialValue === 'boolean' ? (
          <select value={value} onChange={(event) => setValue(event.target.value)} aria-label={name}>
            <option value="true">{t('common.yes')}</option>
            <option value="false">{t('common.no')}</option>
          </select>
        ) : name.startsWith('legal_privacy_override') || name.startsWith('legal_terms_override') ? (
          <textarea
            value={value}
            onChange={(event) => setValue(event.target.value)}
            aria-label={name}
            maxLength={65536}
            rows={14}
            spellCheck={false}
          />
        ) : (
          <input
            type={typeof initialValue === 'number' ? 'number' : 'text'}
            value={value}
            onChange={(event) => setValue(event.target.value)}
            aria-label={name}
            maxLength={512}
          />
        )}
        <button type="submit" className="btn btn-secondary" disabled={busy}>
          {busy ? t('common.working') : t('admin.settings.save')}
        </button>
      </div>
      {error ? <p className="field-error" role="alert">{error}</p> : null}
      {saved ? <p className="inline-success" role="status">{t('admin.settings.saved')}</p> : null}
    </form>
  );
}

export function SettingsPage() {
  const { t } = useTranslation();
  const config = useSiteConfig();

  return (
    <div className="page">
      <PageHeader
        eyebrow={t('app.name')}
        title={t('admin.settings.title')}
        description={t('admin.settings.description')}
      />
      <Card>
        <div className="card-title-row">
          <h2>{t('admin.settings.listTitle')}</h2>
        </div>
        <p className="inline-notice">{t('admin.settings.sensitiveHint')}</p>
        {config.isPending ? (
          <LoadingState />
        ) : config.error ? (
          <ErrorState error={config.error} onRetry={() => void config.refetch()} />
        ) : Object.keys(config.data).length === 0 ? (
          <EmptyState title={t('admin.settings.empty')} body={t('admin.settings.emptyBody')} />
        ) : (
          <div className="config-list">
            {Object.entries(config.data)
              .sort(([left], [right]) => left.localeCompare(right))
              .map(([name, value]) => (
                <ConfigEditor key={name} name={name} initialValue={value} />
              ))}
          </div>
        )}
      </Card>
    </div>
  );
}
