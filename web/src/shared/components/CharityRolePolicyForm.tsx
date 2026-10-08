import { Button } from '@shared/components/ui/Button';
import { useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import {
  charityKeys,
  patchManagedCharityModel,
  type CharityModel,
} from '@shared/operations/charity';
import { useRetainedOperation } from '@shared/operations/useRetainedOperation';
import { responseOutcomeUnknown } from '@shared/operations/api';
import { isForbidden, isUnauthorized } from '@shared/query/http';
import { buildRolePolicy, draftFromRolePolicy, type RolePolicy } from '@shared/rolePolicy';
import { Card, ErrorState } from './States';
import { RolePolicyEditor } from './RolePolicyEditor';

export function CharityRolePolicyForm({
  model,
  refresh,
  onCapabilityLoss,
}: {
  model: CharityModel;
  refresh: () => Promise<unknown>;
  onCapabilityLoss?: () => void;
}) {
  const { t } = useTranslation();
  const [draft, setDraft] = useState(() => draftFromRolePolicy(model.role_policy));
  const [revision, setRevision] = useState(model.revision);
  const save = useRetainedOperation<
    { id: string; revision: string; policy: RolePolicy },
    CharityModel
  >(
    async (input, key, context) => {
      const result = await patchManagedCharityModel(
        'steward',
        input.id,
        { expected_revision: input.revision, role_policy: input.policy },
        key,
      );
      context.commit(() => setRevision(result.revision));
      return result;
    },
    refresh,
    charityKeys.root('steward'),
  );
  const lost = isForbidden(save.error) || isUnauthorized(save.error);
  useEffect(() => {
    if (lost) onCapabilityLoss?.();
  }, [lost, onCapabilityLoss]);
  if (lost) return <p role="alert">{t('common.operations.charity.accessLost')}</p>;
  const unknown = responseOutcomeUnknown(save.error);
  const policy = buildRolePolicy(draft);
  return (
    <Card>
      <h3>{model.full_name}</h3>
      <p>{t('common.rolePolicy.stewardScope')}</p>
      <RolePolicyEditor
        value={draft}
        onChange={(next) => {
          if (save.isPending || unknown) return;
          setDraft(next);
          save.reset();
        }}
        disabled={save.isPending || unknown}
      />
      <div className="ops-actions">
        <Button
          type="button"
          variant="primary"
          disabled={save.isPending || unknown || Boolean(policy.error)}
          onClick={() => {
            if (policy.policy) save.mutate({ id: model.id, revision, policy: policy.policy });
          }}
        >
          {t('common.operations.charity.saveModel')}
        </Button>
        {unknown && save.variables ? (
          <Button
            type="button"

            disabled={save.isPending}
            onClick={() => save.mutate(save.variables!)}
          >
            {t('common.operations.charity.retrySavedModel')}
          </Button>
        ) : null}
        <Button
          type="button"

          disabled={save.isPending || unknown}
          onClick={() => {
            setDraft(draftFromRolePolicy(model.role_policy));
            setRevision(model.revision);
            save.reset();
          }}
        >
          {t('common.operations.charity.reloadModel')}
        </Button>
      </div>
      {save.error ? <ErrorState error={save.error} /> : null}
      {save.isSuccess ? <p role="status">{t('common.rolePolicy.saved')}</p> : null}
      {save.refreshError ? (
        <ErrorState error={save.refreshError} onRetry={() => void save.refresh()} />
      ) : null}
    </Card>
  );
}
