import { Button } from '@shared/components/ui/Button';
import { useTranslation } from 'react-i18next';
import { Note } from './Note';

export type OutcomePresentation =
  | { kind: 'idle' | 'saved' }
  | { kind: 'savedRefreshFailed'; recheck?: () => void }
  | { kind: 'unknown'; recheck?: () => void; oneTimeSecret?: boolean }
  | { kind: 'conflict'; reload?: () => void; inputPreserved?: boolean }
  | { kind: 'failed'; message: string; retry?: () => void }
  | { kind: 'oneTimeMissed'; retry?: () => void };

export function OutcomeNote({
  outcome,
  busy,
  body,
}: {
  outcome: OutcomePresentation;
  busy?: boolean;
  body?: string;
}) {
  const { t } = useTranslation();
  if (outcome.kind === 'idle') return null;
  const action = (label: string, run?: () => void) =>
    run ? (
      <Button
        type="button"
        size="sm"
        disabled={busy}
        onClick={run}
      >
        {label}
      </Button>
    ) : undefined;
  switch (outcome.kind) {
    case 'saved':
      return <Note tone="ok">{t('common.outcome.saved')}</Note>;
    case 'savedRefreshFailed':
      return (
        <Note tone="ok" action={action(t('common.outcome.recheck'), outcome.recheck)}>
          {t('common.outcome.savedRefreshFailed')}
        </Note>
      );
    case 'unknown':
      return (
        <Note
          tone="warn"
          title={t('common.outcome.unknownTitle')}
          action={action(t('common.outcome.recheck'), outcome.recheck)}
        >
          <p>{body ?? t('common.outcome.unknownBody')}</p>
          {outcome.oneTimeSecret ? <p>{t('common.outcome.oneTimeMissedBody')}</p> : null}
        </Note>
      );
    case 'conflict':
      return (
        <Note
          tone="warn"
          title={t('common.outcome.conflictTitle')}
          action={action(t('common.outcome.reload'), outcome.reload)}
        >
          {body ??
            (outcome.inputPreserved === false
              ? t('common.outcome.languageConflictBody')
              : t('common.outcome.conflictBody'))}
        </Note>
      );
    case 'failed':
      return (
        <Note
          tone="bad"
          title={outcome.message}
          action={action(t('common.retry'), outcome.retry)}
        />
      );
    case 'oneTimeMissed':
      return (
        <Note
          tone="bad"
          title={t('common.outcome.oneTimeMissedTitle')}
          action={action(t('common.retry'), outcome.retry)}
        >
          {t('common.outcome.oneTimeMissedBody')}
        </Note>
      );
  }
}
