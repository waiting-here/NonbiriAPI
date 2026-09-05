import { useTranslation } from 'react-i18next';
import { ErrorState, LoadingState, PageHeader } from '@shared/components/States';
import { UserPageGate } from '../components/UserPageGate';
import { useUserSession } from '../data';
import {
  ActivitiesMasterNotice,
  ActivityConnectionNotice,
  ThursdayCard,
  WelfareCard,
} from '../features/economy/ActivitiesPanels';
import { useActivities, useActivityAccountEvents } from '../features/economy/queries';
import '../features/economy/economy.css';

function ActivitiesContent() {
  const { t } = useTranslation();
  const session = useUserSession();
  const activities = useActivities();
  const accountID = session.data?.user.id;
  const stream = useActivityAccountEvents(
    Boolean(activities.data && accountID && !session.isFetching && !session.error),
    accountID ?? '',
    session.dataUpdatedAt,
  );

  return (
    <div className="page economy-page economy-activities-page">
      <PageHeader
        eyebrow={t('user.activities.eyebrow')}
        title={t('user.activities.title')}
        description={t('user.activities.description')}
        icon="activities"
      />
      {activities.isPending && !activities.data ? <LoadingState /> : null}
      {activities.error && !activities.data ? (
        <ErrorState error={activities.error} onRetry={() => void activities.refetch()} />
      ) : null}
      {activities.data ? (
        <>
          <ActivityConnectionNotice
            connection={stream.connection}
            recoveryError={stream.recoveryError}
            reconciled={stream.reconciledAt > 0}
          />
          <ActivitiesMasterNotice snapshot={activities.data} />
          <section className="economy-activities-grid" aria-label={t('user.activities.cardsLabel')}>
            <WelfareCard
              key={`welfare:${accountID}`}
              welfare={activities.data.welfare}
              masterAvailable={activities.data.master.available}
            />
            <ThursdayCard
              key={`thursday:${accountID}`}
              thursday={activities.data.thursday}
              masterAvailable={activities.data.master.available}
            />
          </section>
        </>
      ) : null}
    </div>
  );
}

export function ActivitiesPage() {
  return (
    <UserPageGate>
      <ActivitiesContent />
    </UserPageGate>
  );
}
