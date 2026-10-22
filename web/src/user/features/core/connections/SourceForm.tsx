import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { DISCOVERY_FORMATS, type DiscoveryPreset } from '@shared/connections';
import { Field, Note } from '@shared/components/ui';
import { credentialLabel, CredentialPicker } from './CredentialPicker';
import { ConnectionOperationNotice } from './OperationNotice';
import { connectionAPI, resourcePath } from './api';
import { useConnectionOperation } from './useConnectionOperation';
import { useMetadataOperation } from './useMetadataOperation';
import type { Credential, DiscoverySource } from './types';

export function SourceForm({
  accountId,
  connectionId,
  source,
  preset,
  onSaved,
}: {
  accountId: string;
  connectionId: string;
  source?: DiscoverySource;
  preset?: DiscoveryPreset;
  onSaved: (source: DiscoverySource) => void;
}) {
  const { t } = useTranslation();
  const [name, setName] = useState(source?.name ?? '');
  const [baseURL, setBaseURL] = useState(source?.base_url ?? preset?.base_url ?? '');
  const [format, setFormat] = useState(
    source?.list_format ?? preset?.list_format ?? DISCOVERY_FORMATS[0],
  );
  const [credential, setCredential] = useState<Credential | null>(null);
  const [expectedRevision, setExpectedRevision] = useState(source?.revision ?? '');
  const create = useConnectionOperation(accountId, (result) => {
    if (result.kind === 'discovery_source') onSaved(result.source);
  });
  const edit = useMetadataOperation(
    accountId,
    '/api/discovery-sources/' + resourcePath(source?.id ?? ''),
    'PATCH',
    (signal) => connectionAPI.source(source!.id, signal),
    () => {
      if (source) onSaved(source);
    },
  );
  const operation = source ? edit : create;
  const locked = operation.isPending || operation.outcome === 'unknown';
  return (
    <form
      className="nb-form"
      onSubmit={(event) => {
        event.preventDefault();
        const input = {
          name: name.trim(),
          base_url: baseURL.trim(),
          list_format: format,
          ...(credential ? { credential_id: credential.id } : source ? {} : { credential_id: '' }),
          enabled: source?.enabled ?? true,
        };
        if (source)
          void edit
            .mutateAsync({ ...input, expected_revision: expectedRevision })
            .catch(() => undefined);
        else
          void create
            .run({
              kind: 'discovery_source',
              connectionId,
              input: { ...input, credential_id: credential!.id },
            })
            .catch(() => undefined);
      }}
    >
      <Field label={t('common.connections.name')} optional={t('common.connections.optional')}>
        {(props) => (
          <input
            {...props}
            value={name}
            maxLength={128}
            disabled={locked}
            onChange={(event) => setName(event.target.value)}
          />
        )}
      </Field>
      <Field
        label={t('common.connections.discoveryBaseURL')}
        help={t('common.connections.independentDiscovery')}
      >
        {(props) => (
          <input
            {...props}
            type="url"
            required
            value={baseURL}
            disabled={locked}
            onChange={(event) => setBaseURL(event.target.value)}
          />
        )}
      </Field>
      <Field label={t('common.connections.listFormat')}>
        {(props) => (
          <select
            {...props}
            value={format}
            disabled={locked}
            onChange={(event) => setFormat(event.target.value as typeof format)}
          >
            {DISCOVERY_FORMATS.map((value) => (
              <option key={value} value={value}>
                {t(`common.connections.discoveryFormats.${value}`)}
              </option>
            ))}
          </select>
        )}
      </Field>
      {credential ? (
        <Note>
          <p>
            {credentialLabel(credential)} ·{' '}
            {t('common.connections.versionNumber', { version: credential.credential_version })}
          </p>
          <button
            type="button"
            className="nb-btn nb-btn--secondary"
            disabled={locked}
            onClick={() => setCredential(null)}
          >
            {t('common.connections.changeCredential')}
          </button>
        </Note>
      ) : source ? (
        <Note>
          <p>{t('common.connections.versionNumber', { version: source.credential_version })}</p>
          <p>{t('common.connections.keepCredential')}</p>
        </Note>
      ) : null}
      {!credential ? (
        <CredentialPicker
          accountId={accountId}
          connectionId={connectionId}
          onPick={setCredential}
        />
      ) : null}
      <ConnectionOperationNotice operation={operation} />
      {edit.outcome === 'conflict' ? (
        <Note tone="warn">
          <p>{t('common.connections.conflictDraft')}</p>
          <button
            type="button"
            className="nb-btn nb-btn--secondary"
            onClick={() => {
              setExpectedRevision(source!.revision);
              edit.reset();
            }}
          >
            {t('common.connections.useCurrentRevision')}
          </button>
        </Note>
      ) : null}
      <button
        type="submit"
        className="nb-btn nb-btn--primary"
        disabled={locked || !baseURL.trim() || (!credential && !source)}
      >
        {t('common.connections.saveSource')}
      </button>
    </form>
  );
}
