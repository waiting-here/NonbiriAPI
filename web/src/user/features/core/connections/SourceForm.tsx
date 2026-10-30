import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { DISCOVERY_FORMATS, type DiscoveryPreset } from '@shared/connections';
import { Field, Fields, FormActions, Note } from '@shared/components/ui';
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
  pairSettings = false,
  fixedCredentialId,
}: {
  pairSettings?: boolean;
  fixedCredentialId?: string;
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
          ...(fixedCredentialId
            ? { credential_id: fixedCredentialId }
            : credential
              ? { credential_id: credential.id }
              : source
                ? {}
                : { credential_id: '' }),
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
              input: { ...input, credential_id: fixedCredentialId ?? credential!.id },
            })
            .catch(() => undefined);
      }}
    >
      <Fields>
        {!pairSettings ? (
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
        ) : null}
        <Field
          label={t('common.connections.discoveryBaseURL')}
          size="long"
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
      </Fields>
      {!pairSettings && credential ? (
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
      ) : !pairSettings && source ? (
        <Note>
          <p>{t('common.connections.versionNumber', { version: source.credential_version })}</p>
          <p>{t('common.connections.keepCredential')}</p>
        </Note>
      ) : null}
      {!pairSettings && !credential ? (
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
      <FormActions>
        <button
          type="submit"
          className="nb-btn nb-btn--primary"
          disabled={locked || !baseURL.trim() || (!credential && !source && !fixedCredentialId)}
        >
          {t(pairSettings ? 'user.services.saveListSettings' : 'common.connections.saveSource')}
        </button>
      </FormActions>
    </form>
  );
}
