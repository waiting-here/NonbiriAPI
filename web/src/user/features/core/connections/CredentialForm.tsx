import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Field, Note } from '@shared/components/ui';
import { ConnectionOperationNotice } from './OperationNotice';
import { useConnectionOperation } from './useConnectionOperation';
import type { Credential } from './types';

export function CredentialCreateForm({
  accountId,
  connectionId,
  onSaved,
  replace,
}: {
  accountId: string;
  connectionId: string;
  onSaved: (value: Credential) => void;
  replace?: Credential;
}) {
  const { t } = useTranslation();
  const [secret, setSecret] = useState('');
  const [note, setNote] = useState('');
  const [owned, setOwned] = useState(false);
  const operation = useConnectionOperation(
    accountId,
    (result) => {
      if (result.kind === 'credential' || result.kind === 'credential_version')
        onSaved(result.credential);
    },
    () => {
      setSecret('');
      setOwned(false);
    },
  );
  const locked = operation.isPending || operation.outcome === 'unknown';
  return (
    <form
      className="nb-form"
      onSubmit={(event) => {
        event.preventDefault();
        const intent = replace
          ? {
              kind: 'credential_version' as const,
              credentialId: replace.id,
              expectedRevision: replace.revision,
            }
          : {
              kind: 'credential' as const,
              connectionId,
              input: { note: note.trim(), ownership_confirmed: true as const },
            };
        void operation.run(intent, secret).catch(() => undefined);
      }}
    >
      {!replace ? (
        <Field
          label={t('common.connections.credentialName')}
          optional={t('common.connections.optional')}
        >
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
      ) : (
        <Note>{t('common.connections.rotationEffect')}</Note>
      )}
      <Field
        label={replace ? t('common.connections.newSecret') : t('common.connections.secret')}
        help={t('common.connections.secretHelp')}
      >
        {(props) => (
          <input
            {...props}
            type="password"
            autoComplete="new-password"
            value={secret}
            required
            disabled={locked}
            onChange={(event) => setSecret(event.target.value)}
          />
        )}
      </Field>
      <Field label={t('common.connections.ownership')}>
        {(props) => (
          <input
            {...props}
            type="checkbox"
            checked={owned}
            disabled={locked}
            required
            onChange={(event) => setOwned(event.target.checked)}
          />
        )}
      </Field>
      <ConnectionOperationNotice operation={operation} />
      <div className="nb-actions">
        <button
          type="submit"
          className="nb-btn nb-btn--primary"
          disabled={locked || !secret || !owned}
        >
          {replace ? t('common.connections.rotate') : t('common.connections.saveCredential')}
        </button>
      </div>
    </form>
  );
}
