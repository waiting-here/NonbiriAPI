import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Field } from '@shared/components/ui';
import { ConnectionOperationNotice } from './OperationNotice';
import { useConnectionOperation } from './useConnectionOperation';
import type { Connection } from './types';

export function ConnectionCreateForm({
  accountId,
  onSaved,
}: {
  accountId: string;
  onSaved: (value: Connection) => void;
}) {
  const { t } = useTranslation();
  const [name, setName] = useState('');
  const [note, setNote] = useState('');
  const operation = useConnectionOperation(accountId, (result) => {
    if (result.kind === 'connection') onSaved(result.connection);
  });
  const locked = operation.isPending || operation.outcome === 'unknown';
  return (
    <form
      className="nb-form"
      onSubmit={(event) => {
        event.preventDefault();
        void operation
          .run({ kind: 'connection', input: { name: name.trim(), note: note.trim() } })
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
            placeholder={t('common.connections.nameExample')}
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
      <ConnectionOperationNotice operation={operation} />
      <div className="nb-actions">
        <button type="submit" className="nb-btn nb-btn--primary" disabled={locked || !name.trim()}>
          {t('common.connections.create')}
        </button>
      </div>
    </form>
  );
}
