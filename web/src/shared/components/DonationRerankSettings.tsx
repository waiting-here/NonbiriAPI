import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { responseOutcomeUnknown } from '@shared/operations/api';
import { mutateDonationUse, type DonationUse, type UseRole } from '@shared/operations/donationUses';
import {
  useRetainedOperation,
  type OperationContext,
} from '@shared/operations/useRetainedOperation';
import { ErrorState } from './States';
import { Field, Fields, FormActions, Note, OutcomeNote } from './ui';
import { RerankPathField, RerankURL } from './RerankPathField';
import { rerankURLPreview } from '@shared/rerankPath';

type Input = { expected_revision: string; rerank_path_override: string | null };

export function DonationRerankSettings({
  role,
  donationId,
  use,
  refresh,
  disabled = false,
}: {
  role: UseRole;
  donationId: string;
  use: DonationUse;
  refresh: () => Promise<void>;
  disabled?: boolean;
}) {
  const { t } = useTranslation();
  const [baseline, setBaseline] = useState(use);
  const [override, setOverride] = useState(use.rerank_path_override ?? null);
  const selected = override === null ? null : override.trim();
  const dirty = selected !== (baseline.rerank_path_override ?? null);
  const root = role === 'admin' ? ['admin', 'operations'] : ['user'];
  const write = async (input: Input, key: string, context: OperationContext) => {
    await mutateDonationUse(role, donationId, use.id, 'patch', input, key, context.signal);
  };
  const operation = useRetainedOperation<Input, void>(
    write,
    async (input, error, context) => {
      if (error && responseOutcomeUnknown(error) && context.operationKey)
        await write(input, context.operationKey, context);
      await refresh();
      if (error && responseOutcomeUnknown(error)) return { operationConfirmed: true };
    },
    root,
  );
  if (
    use.revision !== baseline.revision &&
    !operation.isPending &&
    operation.outcome !== 'unknown' &&
    (operation.isSuccess || !dirty)
  ) {
    setBaseline(use);
    setOverride(use.rerank_path_override ?? null);
  }
  if (use.native_service !== 'rerank' || !use.effective_rerank_url) return null;
  if (role === 'owner' || !use.capabilities.can_edit_rerank_path)
    return <RerankURL value={use.effective_rerank_url} />;

  const snapshot = use.approved ?? use.pending;
  const locked =
    disabled ||
    operation.isPending ||
    operation.outcome === 'unknown' ||
    Boolean(operation.refreshError);
  const change = (value: string | null) => {
    setOverride(value);
    operation.reset();
  };
  const preview =
    selected === null
      ? (snapshot?.rerank_url ?? '')
      : selected === ''
        ? (use.default_rerank_url ?? '')
        : rerankURLPreview(use.effective_rerank_url, selected);
  return (
    <form
      className="nb-form"
      onSubmit={(event) => {
        event.preventDefault();
        operation.mutate({ expected_revision: baseline.revision, rerank_path_override: selected });
      }}
    >
      <Fields columns={1} legend={t('common.nativeServices.charityRerankSettings')}>
        <Field
          label={t('common.nativeServices.charityRerankSource')}
          help={t('common.nativeServices.charityRerankHelp')}
        >
          {(props) => (
            <select
              {...props}
              value={override === null ? 'inherit' : 'custom'}
              disabled={locked}
              onChange={(event) =>
                change(event.target.value === 'inherit' ? null : (use.effective_rerank_path ?? ''))
              }
            >
              <option value="inherit">{t('common.nativeServices.inheritDonatedRerank')}</option>
              <option value="custom">{t('common.nativeServices.overrideCharityRerank')}</option>
            </select>
          )}
        </Field>
        {override !== null ? (
          <RerankPathField value={override} onChange={change} baseURL="" disabled={locked} />
        ) : null}
        <RerankURL value={preview} />
      </Fields>
      {operation.error ? <ErrorState error={operation.error} /> : null}
      {operation.outcome === 'unknown' ? (
        <Note tone="warn">
          <p>{t('common.connections.unknownResult')}</p>
          <button
            type="button"
            className="nb-btn nb-btn--secondary"
            onClick={() => void operation.check()}
            disabled={operation.isPending}
          >
            {t('common.connections.checkResult')}
          </button>
        </Note>
      ) : null}
      {operation.refreshError ? (
        operation.isSuccess ? (
          <OutcomeNote
            outcome={{ kind: 'savedRefreshFailed', recheck: () => void operation.refresh() }}
          />
        ) : (
          <ErrorState error={operation.refreshError} />
        )
      ) : operation.isSuccess ? (
        <Note tone="ok">{t('common.outcome.saved')}</Note>
      ) : null}
      {operation.outcome === 'conflict' ? (
        <button
          type="button"
          className="nb-btn nb-btn--secondary"
          onClick={() => {
            setBaseline(use);
            operation.reset();
          }}
        >
          {t('common.connections.useCurrentRevision')}
        </button>
      ) : null}
      <FormActions>
        <button
          type="submit"
          className="nb-btn nb-btn--primary"
          disabled={locked || !dirty || operation.outcome === 'conflict'}
        >
          {t('common.nativeServices.saveCharityRerank')}
        </button>
      </FormActions>
    </form>
  );
}
