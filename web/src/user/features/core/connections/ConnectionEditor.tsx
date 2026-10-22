import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Field, Note } from '@shared/components/ui';
import { ConfirmDialog } from '@shared/components/ConfirmDialog';
import { ConnectionOperationNotice } from './OperationNotice';
import { connectionAPI, resourcePath } from './api';
import { useMetadataOperation } from './useMetadataOperation';
import type { Connection } from './types';

export function ConnectionEditor({
  accountId,
  connection,
  onSaved,
  onDeleted,
}: {
  accountId: string;
  connection: Connection;
  onSaved: () => void;
  onDeleted: () => void;
}) {
  const { t } = useTranslation();
  const [name, setName] = useState(connection.name);
  const [note, setNote] = useState(connection.note);
  const [expectedRevision, setExpectedRevision] = useState(connection.revision);
  const [deleting, setDeleting] = useState<string | null>(null);
  const path = '/api/connections/' + resourcePath(connection.id);
  const read = (signal: AbortSignal) => connectionAPI.get(connection.id, signal);
  const save = useMetadataOperation(accountId, path, 'PATCH', read, onSaved);
  const remove = useMetadataOperation(accountId, path, 'DELETE', read, onDeleted);
  const locked = save.isPending || save.outcome === 'unknown';
  return (
    <div className="nb-stack">
      <form
        className="nb-form"
        onSubmit={(event) => {
          event.preventDefault();
          void save
            .mutateAsync({
              name: name.trim(),
              note: note.trim(),
              expected_revision: expectedRevision,
            })
            .catch(() => undefined);
        }}
      >
        <Field label={t('common.connections.name')}>
          {(props) => (
            <input
              {...props}
              value={name}
              maxLength={128}
              required
              disabled={locked}
              onChange={(event) => setName(event.target.value)}
            />
          )}
        </Field>
        <Field label={t('common.connections.note')} optional={t('common.connections.optional')}>
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
        <ConnectionOperationNotice operation={save} />
        {save.outcome === 'conflict' ? (
          <Note tone="warn">
            <p>{t('common.connections.conflictDraft')}</p>
            <p>{t('common.connections.currentName', { name: connection.name })}</p>
            <button
              type="button"
              className="nb-btn nb-btn--secondary"
              disabled={save.isPending}
              onClick={() => {
                setExpectedRevision(connection.revision);
                save.reset();
              }}
            >
              {t('common.connections.useCurrentRevision')}
            </button>
          </Note>
        ) : null}
        <div className="nb-actions">
          <button
            type="submit"
            className="nb-btn nb-btn--primary"
            disabled={locked || !name.trim()}
          >
            {t('common.connections.save')}
          </button>
          <button
            type="button"
            className="nb-btn nb-btn--danger-outline"
            disabled={locked}
            onClick={() => {
              remove.reset();
              setDeleting(connection.revision);
            }}
          >
            {t('common.connections.deleteConnection')}
          </button>
        </div>
      </form>
      <ConfirmDialog
        open={deleting !== null}
        title={t('common.connections.deleteConnection')}
        description={t('common.connections.deleteConnectionEffect')}
        confirmLabel={t('common.connections.delete')}
        danger
        busy={remove.isPending}
        confirmDisabled={remove.outcome === 'unknown' || remove.outcome === 'conflict'}
        onCancel={() => setDeleting(null)}
        onConfirm={() => {
          if (deleting)
            void remove.mutateAsync({ expected_revision: deleting }).catch(() => undefined);
        }}
      >
        <ConnectionOperationNotice operation={remove} />
      </ConfirmDialog>
    </div>
  );
}
