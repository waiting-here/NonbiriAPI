import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Field, Fold, Note, Segmented } from '@shared/components/ui';
import type { DiscoveryPreset } from '@shared/connections';
import { supportsChatStoragePolicy } from '@shared/operations/connectorPolicy';
import { KeyLimitFields } from '@shared/components/KeyRoutingLimits';
import type { Endpoint, EndpointKey } from '../types';
import { credentialLabel, CredentialPicker } from './CredentialPicker';
import { ConnectionOperationNotice } from './OperationNotice';
import { useConnectionOperation } from './useConnectionOperation';
import { ServiceModelSetup } from './ServiceModelSetup';
import type { Credential } from './types';

export function CredentialGrantForm({
  accountId,
  connectionId,
  endpoint,
  discoveryPreset,
  onSaved,
}: {
  accountId: string;
  connectionId: string;
  endpoint: Endpoint;
  discoveryPreset?: DiscoveryPreset;
  onSaved?: (key: EndpointKey) => void;
}) {
  const { t } = useTranslation();
  const [mode, setMode] = useState<'existing' | 'new'>('new');
  const [credential, setCredential] = useState<Credential | null>(null);
  const [secret, setSecret] = useState('');
  const [note, setNote] = useState('');
  const [concurrency, setConcurrency] = useState('0');
  const [rpm, setRPM] = useState('0');
  const [forceStoreFalse, setForceStoreFalse] = useState(false);
  const [saved, setSaved] = useState<EndpointKey | null>(null);
  const operation = useConnectionOperation(
    accountId,
    (result) => {
      if (result.kind !== 'key') return;
      setSaved(result.key);
      onSaved?.(result.key);
    },
    () => setSecret(''),
  );
  const locked =
    operation.isPending ||
    operation.outcome === 'unknown' ||
    operation.outcome === 'refresh-failed';
  if (saved)
    return (
      <div className="nb-stack">
        <Note tone="ok">{t('user.services.keyAdded')}</Note>
        <ServiceModelSetup accountId={accountId} entry={endpoint} entryKey={saved} autoRefresh />
      </div>
    );
  return (
    <div className="nb-stack">
      <Segmented
        label={t('common.connections.credential')}
        value={mode}
        options={[
          { value: 'new', label: t('common.connections.newCredential') },
          { value: 'existing', label: t('common.connections.existingCredential') },
        ]}
        onChange={setMode}
        disabled={locked}
      />
      {mode === 'existing' ? (
        credential ? (
          <Note>
            {credentialLabel(credential)}
            <button
              type="button"
              className="nb-btn nb-btn--ghost"
              disabled={locked}
              onClick={() => setCredential(null)}
            >
              {t('common.connections.changeCredential')}
            </button>
          </Note>
        ) : (
          <CredentialPicker
            accountId={accountId}
            connectionId={connectionId}
            onPick={setCredential}
          />
        )
      ) : null}
      <form
        className="nb-form"
        onSubmit={(event) => {
          event.preventDefault();
          if (locked) return;
          void operation
            .run(
              {
                kind: 'key',
                endpointId: endpoint.id,
                input: {
                  ...(mode === 'existing' ? { credential_id: credential!.id } : {}),
                  native_services: endpoint.native_services,
                  note: note.trim(),
                  enabled: true,
                  ownership_confirmed: true,
                  force_store_false:
                    supportsChatStoragePolicy(endpoint.connector_type) && forceStoreFalse,
                  max_concurrency: Number(concurrency),
                  max_rpm: Number(rpm),
                },
              },
              mode === 'new' ? secret : undefined,
            )
            .catch(() => undefined);
        }}
      >
        {mode === 'new' ? (
          <>
            <Field label={t('common.connections.secret')} help={t('common.connections.secretHelp')}>
              {(props) => (
                <input
                  {...props}
                  type="password"
                  autoComplete="new-password"
                  required
                  value={secret}
                  disabled={locked}
                  onChange={(event) => setSecret(event.target.value)}
                />
              )}
            </Field>
            <p className="nb-small nb-muted">{t('user.services.keyOwnership')}</p>
          </>
        ) : null}
        <Fold title={t('user.services.keySettings')}>
          <Field
            label={t('common.connections.credentialName')}
            optional={t('common.connections.optional')}
          >
            {(props) => (
              <input
                {...props}
                value={note}
                disabled={locked}
                onChange={(event) => setNote(event.target.value)}
              />
            )}
          </Field>
          <KeyLimitFields
            concurrency={concurrency}
            rpm={rpm}
            onConcurrency={setConcurrency}
            onRPM={setRPM}
            disabled={locked}
          />
          {supportsChatStoragePolicy(endpoint.connector_type) ? (
            <Field label={t('user.core.endpoints.storePolicy')}>
              {(props) => (
                <input
                  {...props}
                  type="checkbox"
                  checked={forceStoreFalse}
                  disabled={locked}
                  onChange={(event) => setForceStoreFalse(event.target.checked)}
                />
              )}
            </Field>
          ) : null}
          {discoveryPreset ? (
            <p className="nb-small nb-muted">{t('user.services.listSettingsAfterSave')}</p>
          ) : null}
        </Fold>
        <ConnectionOperationNotice operation={operation} />
        <button
          type="submit"
          className="nb-btn nb-btn--primary"
          disabled={locked || (mode === 'new' ? !secret.trim() : !credential)}
        >
          {t('user.services.addKeyAndFetch')}
        </button>
      </form>
    </div>
  );
}
