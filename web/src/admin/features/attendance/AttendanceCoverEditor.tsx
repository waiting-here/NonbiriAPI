import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Field, Fields } from '@shared/components/ui';
import type { AttendanceCover } from '@shared/attendance/api';
import { CampaignCover } from '@shared/attendance/CampaignCover';
import { campaignCoverChoices } from '@shared/attendance/coverChoices';
import { isPublicLogoURL } from '@shared/query/publicConfig';
import { useArtworkPolicy } from '@shared/artwork/context';

const maxImageURL = 2048;
type CoverSource = 'default' | AttendanceCover['kind'];

export function AttendanceCoverEditor({
  cover,
  savedCover,
  disabled,
  onChange,
}: {
  cover?: AttendanceCover;
  savedCover?: AttendanceCover;
  disabled: boolean;
  onChange: (value?: AttendanceCover) => void;
}) {
  const { t, i18n } = useTranslation();
  const policy = useArtworkPolicy();
  const [failedURL, setFailedURL] = useState('');
  const english = i18n.resolvedLanguage?.startsWith('en') ?? false;
  const externalURL = cover?.kind === 'external' ? cover.value.trim() : '';
  const externalError =
    externalURL &&
    (!/^https:\/\//i.test(externalURL) ||
      !isPublicLogoURL(externalURL) ||
      new TextEncoder().encode(externalURL).length > maxImageURL)
      ? t('admin.attendance.cover.invalidUrl')
      : undefined;
  const choices =
    cover?.kind === 'pool' || cover?.kind === 'collection'
      ? campaignCoverChoices(cover.kind, english)
      : [];
  const preview =
    cover?.kind === 'external'
      ? externalURL && !externalError
        ? { ...cover, value: externalURL }
        : undefined
      : cover;
  return (
    <fieldset className="attendance-editor__rules" disabled={disabled}>
      <legend>{t('admin.attendance.cover.title')}</legend>
      <div className="attendance-editor__cover-layout">
        <Fields columns={1}>
          <Field label={t('admin.attendance.cover.source')}>
            {(props) => (
              <select
                {...props}
                value={cover?.kind ?? 'default'}
                onChange={(event) => {
                  const kind = event.currentTarget.value as CoverSource;
                  setFailedURL('');
                  onChange(
                    kind === 'default'
                      ? undefined
                      : { kind, value: '', fit: cover?.fit ?? 'cover' },
                  );
                }}
              >
                <option value="default">{t('admin.attendance.cover.default')}</option>
                <option value="pool">{t('admin.attendance.cover.pool')}</option>
                <option value="collection">{t('admin.attendance.cover.collection')}</option>
                <option value="external">{t('admin.attendance.cover.external')}</option>
              </select>
            )}
          </Field>
          {cover?.kind === 'external' && (
            <Field label={t('admin.attendance.cover.url')} error={externalError}>
              {(props) => (
                <input
                  {...props}
                  ref={(node) => {
                    node?.setCustomValidity(externalError ?? '');
                  }}
                  type="url"
                  maxLength={maxImageURL}
                  required
                  value={cover.value}
                  onChange={(event) => {
                    setFailedURL('');
                    onChange({ ...cover, value: event.currentTarget.value });
                  }}
                />
              )}
            </Field>
          )}
          {(cover?.kind === 'pool' || cover?.kind === 'collection') && (
            <Field label={t('admin.attendance.cover.artwork')}>
              {(props) => (
                <select
                  {...props}
                  required
                  value={cover.value}
                  onChange={(event) => onChange({ ...cover, value: event.currentTarget.value })}
                >
                  <option value="" disabled>
                    {t('admin.attendance.cover.chooseArtwork')}
                  </option>
                  {cover.value && !choices.some(({ value }) => value === cover.value) && (
                    <option value={cover.value}>{t('admin.attendance.cover.savedArtwork')}</option>
                  )}
                  {choices.map(({ value, label, artworkKey }) => (
                    <option
                      key={value}
                      value={value}
                      disabled={
                        !policy.allowed(artworkKey) &&
                        !(savedCover?.kind === cover.kind && savedCover.value === value)
                      }
                    >
                      {label}
                    </option>
                  ))}
                </select>
              )}
            </Field>
          )}
          {cover && (
            <Field label={t('admin.attendance.cover.fit')}>
              {(props) => (
                <select
                  {...props}
                  value={cover.fit}
                  onChange={(event) =>
                    onChange({
                      ...cover,
                      fit: event.currentTarget.value === 'contain' ? 'contain' : 'cover',
                    })
                  }
                >
                  <option value="cover">{t('admin.attendance.cover.fill')}</option>
                  <option value="contain">{t('admin.attendance.cover.contain')}</option>
                </select>
              )}
            </Field>
          )}
        </Fields>
        <figure className="attendance-editor__cover-preview">
          <figcaption>{t('admin.attendance.cover.preview')}</figcaption>
          <CampaignCover
            cover={preview}
            className="attendance-editor__cover-image"
            onLoadError={
              externalURL ? (failed) => setFailedURL(failed ? externalURL : '') : undefined
            }
          />
          {externalURL && failedURL === externalURL && (
            <p className="nb-small nb-muted" role="status">
              {t('admin.attendance.cover.loadError')}
            </p>
          )}
        </figure>
      </div>
    </fieldset>
  );
}
