import { useState } from 'react';
import { useSearchParams } from 'react-router';
import { useTranslation } from 'react-i18next';
import { useQuery } from '@tanstack/react-query';
import { ConfirmDialog } from '@shared/components/ConfirmDialog';
import { ExpandablePanel } from '@shared/components/ui/ExpandablePanel';
import { Field, Note, Panel, PanelBody, PanelHead } from '@shared/components/ui';
import { CoreErrorPanel, CoreLoading } from '../components';
import { connectionAPI, resourcePath } from './api';
import { ConnectionResourceList, connectionKeys } from './ResourceList';
import { CredentialCreateForm } from './CredentialForm';
import { credentialLabel } from './CredentialPicker';
import { CredentialImpactPanel } from './CredentialImpactPanel';
import { CredentialVersions } from './CredentialVersions';
import { ConnectionOperationNotice } from './OperationNotice';
import { useMetadataOperation } from './useMetadataOperation';
import type { Credential } from './types';

export function CredentialsPanel({
  accountId,
  connectionId,
}: {
  accountId: string;
  connectionId: string;
}) {
  const { t } = useTranslation();
  const [params, setParams] = useSearchParams();
  const [creating, setCreating] = useState(false);
  const selected = params.get('credential_id');
  const select = (value: string | null) =>
    setParams((previous) => {
      const next = new URLSearchParams(previous);
      if (value) next.set('credential_id', value);
      else next.delete('credential_id');
      return next;
    });
  if (selected)
    return (
      <CredentialDetail
        key={selected}
        accountId={accountId}
        id={selected}
        onBack={() => select(null)}
      />
    );
  return (
    <div className="nb-stack">
      {creating ? (
        <ExpandablePanel
          open
          onClose={() => setCreating(false)}
          title={t('common.connections.newCredential')}
          closeLabel={t('common.connections.close')}
        >
          <CredentialCreateForm
            accountId={accountId}
            connectionId={connectionId}
            onSaved={(value) => {
              setCreating(false);
              select(value.id);
            }}
          />
        </ExpandablePanel>
      ) : null}
      <ConnectionResourceList
        accountId={accountId}
        scope={connectionId}
        listType="connection-credentials"
        title={t('common.connections.credentials')}
        searchPlaceholder={t('common.connections.credentialSearch')}
        load={(window, q, signal) => connectionAPI.credentials(connectionId, window, q, signal)}
        rowKey={(row) => row.id}
        actions={
          <button
            type="button"
            className="nb-btn nb-btn--primary"
            onClick={() => setCreating(true)}
          >
            {t('common.connections.newCredential')}
          </button>
        }
        columns={[
          {
            key: 'name',
            header: t('common.connections.credentialName'),
            cell: 'title',
            render: (row) => (
              <div className="nb-stack nb-stack--sm">
                <strong>{credentialLabel(row)}</strong>
                <small className="nb-mono">
                  {row.display_head}••••{row.display_tail}
                </small>
              </div>
            ),
          },
          {
            key: 'version',
            header: t('common.connections.version'),
            cell: 'meta',
            render: (row) =>
              t('common.connections.versionNumber', { version: row.credential_version }),
          },
          {
            key: 'enabled',
            header: t('common.connections.status'),
            cell: 'status',
            render: (row) =>
              row.enabled ? t('common.connections.enabled') : t('common.connections.disabled'),
          },
          {
            key: 'manage',
            header: t('common.connections.actions'),
            cell: 'action',
            render: (row) => (
              <button
                type="button"
                className="nb-btn nb-btn--secondary"
                onClick={() => select(row.id)}
              >
                {t('common.connections.manage')}
              </button>
            ),
          },
        ]}
      />
    </div>
  );
}
export function CredentialDetail({
  accountId,
  id,
  onBack,
}: {
  accountId: string;
  id: string;
  onBack: () => void;
}) {
  const { t } = useTranslation();
  const back = (
    <button type="button" className="nb-btn nb-btn--secondary" onClick={onBack}>
      {t('common.connections.backToCredentials')}
    </button>
  );
  const query = useQuery({
    queryKey: [...connectionKeys.root(accountId), 'credential', id],
    queryFn: ({ signal }) => connectionAPI.credential(id, signal),
  });
  if (query.isPending)
    return (
      <div className="nb-stack">
        {back}
        <CoreLoading />
      </div>
    );
  if (query.error || !query.data)
    return (
      <div className="nb-stack">
        {back}
        <CoreErrorPanel error={query.error} onRetry={() => void query.refetch()} />
      </div>
    );
  return (
    <CredentialDetails key={id} accountId={accountId} credential={query.data} onBack={onBack} />
  );
}

function CredentialDetails({
  accountId,
  credential,
  onBack,
}: {
  accountId: string;
  credential: Credential;
  onBack: () => void;
}) {
  const { t } = useTranslation();
  const [rotating, setRotating] = useState(false);
  const [editing, setEditing] = useState(false);
  const [readyRevision, setReadyRevision] = useState('');
  const [danger, setDanger] = useState<{ kind: 'disable' | 'delete'; revision: string } | null>(
    null,
  );
  const path = '/api/credentials/' + resourcePath(credential.id);
  const read = (signal: AbortSignal) => connectionAPI.credential(credential.id, signal);
  const patch = useMetadataOperation(accountId, path, 'PATCH', read, () => setDanger(null));
  const remove = useMetadataOperation(accountId, path, 'DELETE', read, onBack);
  const action = danger?.kind === 'delete' ? remove : patch;
  return (
    <div className="nb-stack">
      <button type="button" className="nb-btn nb-btn--ghost" onClick={onBack}>
        {t('common.connections.backToCredentials')}
      </button>
      <Panel>
        <PanelHead
          title={credentialLabel(credential)}
          actions={
            <>
              <button
                type="button"
                className="nb-btn nb-btn--secondary"
                onClick={() => setEditing((value) => !value)}
              >
                {t('common.connections.editName')}
              </button>
              <button
                type="button"
                className="nb-btn nb-btn--primary"
                onClick={() => setRotating((value) => !value)}
              >
                {t('common.connections.rotate')}
              </button>
            </>
          }
        />
        <PanelBody>
          <p className="nb-mono">
            {credential.display_head}••••{credential.display_tail}
          </p>
          <p>
            {t('common.connections.versionNumber', { version: credential.credential_version })} ·{' '}
            {credential.enabled
              ? t('common.connections.enabled')
              : t('common.connections.disabled')}
          </p>
          <div className="nb-actions">
            <button
              type="button"
              className={
                'nb-btn ' + (credential.enabled ? 'nb-btn--danger-outline' : 'nb-btn--secondary')
              }
              disabled={patch.isPending}
              onClick={() => {
                if (credential.enabled) {
                  patch.reset();
                  setDanger({ kind: 'disable', revision: credential.revision });
                } else
                  void patch
                    .mutateAsync({ enabled: true, expected_revision: credential.revision })
                    .catch(() => undefined);
              }}
            >
              {credential.enabled
                ? t('common.connections.disableCredential')
                : t('common.connections.enable')}
            </button>
            <button
              type="button"
              className="nb-btn nb-btn--danger-outline"
              onClick={() => {
                remove.reset();
                setDanger({ kind: 'delete', revision: credential.revision });
              }}
            >
              {t('common.connections.deleteCredential')}
            </button>
          </div>
          <ConnectionOperationNotice operation={patch} />
        </PanelBody>
      </Panel>
      {editing ? (
        <ExpandablePanel
          open
          onClose={() => setEditing(false)}
          title={t('common.connections.editName')}
          closeLabel={t('common.connections.close')}
        >
          <CredentialNoteForm
            accountId={accountId}
            credential={credential}
            onSaved={() => setEditing(false)}
          />
        </ExpandablePanel>
      ) : null}
      {rotating ? (
        <ExpandablePanel
          open
          onClose={() => setRotating(false)}
          title={t('common.connections.rotate')}
          closeLabel={t('common.connections.close')}
        >
          <CredentialImpactPanel
            accountId={accountId}
            credentialId={credential.id}
            revision={credential.revision}
            onReady={setReadyRevision}
          />
          {readyRevision === credential.revision ? (
            <CredentialCreateForm
              accountId={accountId}
              connectionId={credential.connection_id ?? ''}
              replace={credential}
              onSaved={() => setRotating(false)}
            />
          ) : null}
        </ExpandablePanel>
      ) : null}
      <CredentialVersions accountId={accountId} credentialId={credential.id} />
      <ConfirmDialog
        open={danger !== null}
        title={
          danger?.kind === 'delete'
            ? t('common.connections.deleteCredential')
            : t('common.connections.disableCredential')
        }
        description={t('common.connections.allCredentialUsesEffect')}
        confirmLabel={
          danger?.kind === 'delete'
            ? t('common.connections.delete')
            : t('common.connections.disable')
        }
        danger
        busy={action.isPending}
        confirmDisabled={action.outcome === 'unknown' || action.outcome === 'conflict'}
        onCancel={() => setDanger(null)}
        onConfirm={() => {
          if (danger)
            void action
              .mutateAsync({
                expected_revision: danger.revision,
                ...(danger.kind === 'disable' ? { enabled: false } : {}),
              })
              .catch(() => undefined);
        }}
      >
        <ConnectionOperationNotice operation={action} />
      </ConfirmDialog>
    </div>
  );
}
function CredentialNoteForm({
  accountId,
  credential,
  onSaved,
}: {
  accountId: string;
  credential: Credential;
  onSaved: () => void;
}) {
  const { t } = useTranslation();
  const [note, setNote] = useState(credential.note);
  const [expected, setExpected] = useState(credential.revision);
  const operation = useMetadataOperation(
    accountId,
    '/api/credentials/' + resourcePath(credential.id),
    'PATCH',
    (signal) => connectionAPI.credential(credential.id, signal),
    onSaved,
  );
  const locked = operation.isPending || operation.outcome === 'unknown';
  return (
    <form
      className="nb-form"
      onSubmit={(event) => {
        event.preventDefault();
        void operation
          .mutateAsync({ note: note.trim(), expected_revision: expected })
          .catch(() => undefined);
      }}
    >
      <Field label={t('common.connections.credentialName')}>
        {(props) => (
          <input
            {...props}
            value={note}
            maxLength={1024}
            disabled={locked}
            onChange={(event) => setNote(event.target.value)}
          />
        )}
      </Field>
      <ConnectionOperationNotice operation={operation} />
      {operation.outcome === 'conflict' ? (
        <Note tone="warn">
          <p>{t('common.connections.conflictDraft')}</p>
          <button
            type="button"
            className="nb-btn nb-btn--secondary"
            onClick={() => {
              setExpected(credential.revision);
              operation.reset();
            }}
          >
            {t('common.connections.useCurrentRevision')}
          </button>
        </Note>
      ) : null}
      <button type="submit" className="nb-btn nb-btn--primary" disabled={locked}>
        {t('common.connections.save')}
      </button>
    </form>
  );
}
