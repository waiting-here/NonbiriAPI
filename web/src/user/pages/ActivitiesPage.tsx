import { useState, type ReactNode } from 'react';
import { useTranslation } from 'react-i18next';
import { Link } from 'react-router';
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
import { LoanCard } from '../features/economy/LoanCard';
import { LimitedActivitiesSection } from '../features/limitedactivities/LimitedActivities';
import '../features/economy/economy.css';
import '../features/economy/activities.css';

function ActivitySlot({
  title,
  available,
  children,
}: {
  title: string;
  available: boolean;
  children: ReactNode;
}) {
  const { t } = useTranslation();
  const [state, setState] = useState({ available, expanded: false });
  if (state.available !== available) setState({ available, expanded: false });
  return (
    <details
      className={`activity-slot nb-fold${available ? ' is-available' : ''}`}
      open={available || state.expanded}
      onToggle={(event) => {
        if (!available) setState({ available, expanded: event.currentTarget.open });
      }}
    >
      <summary>
        <span className="nb-fold__title">
          <strong>{title}</strong>
        </span>
        <span className="nb-badge nb-badge--plain">
          {t('user.activities.presentation.unavailable')}
        </span>
      </summary>
      <div className="activity-slot__body">{children}</div>
    </details>
  );
}

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
        title={t('user.activities.title')}
        description={t('user.activities.description')}
        icon="activities"
        actions={
          <Link className="btn btn-secondary" to="/credits">
            {t('user.activities.presentation.records')}
          </Link>
        }
      />
      <LimitedActivitiesSection hideEmpty />
      <h2>{t('common.activities.permanent')}</h2>
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
            <LoanCard
              key={`loan:${accountID}`}
              account={accountID ?? ''}
              loan={activities.data.loan}
              masterAvailable={activities.data.master.available}
            />
            <ActivitySlot
              title={t('user.activities.welfare.title')}
              available={activities.data.master.available}
            >
              <WelfareCard
                key={`welfare:${accountID}`}
                welfare={activities.data.welfare}
                masterAvailable={activities.data.master.available}
              />
            </ActivitySlot>
            <ActivitySlot
              title={t('user.activities.thursday.title')}
              available={activities.data.master.available}
            >
              <ThursdayCard
                key={`thursday:${accountID}`}
                thursday={activities.data.thursday}
                masterAvailable={activities.data.master.available}
              />
            </ActivitySlot>
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
