import { useTranslation } from 'react-i18next';
import { Link, useLocation, useParams } from 'react-router';
import { useSearchState } from '@shared/operations/useSearchState';
import { EmptyState, ErrorState, LoadingState, PageHeader } from '@shared/components/States';
import { Tabs } from '@shared/components/ui';
import { isNotFoundError } from '@shared/query/http';
import { usePublicConfig } from '@shared/query/publicConfig';
import { listReturnPath } from '@shared/operations/listReturn';
import { UserPageGate } from '../components/UserPageGate';
import { useUserSession } from '../data';
import { Leaderboard } from '../games/ranking/Leaderboard';
import { CharityCatalogPanel } from '../features/economy/CharityCatalogPanel';
import {
  CharitySafetyNotice,
  DonationCard,
  DonationComposer,
  DonationIntakePanel,
} from '../features/economy/CharityPanels';
import { OwnerDonationKeys, OwnerDonationsPanel } from '../features/economy/OwnerDonationsPanel';
import { useCharityCapability, useDonation } from '../features/economy/queries';
import { CreditAmount } from '../features/economy/ExactValue';
import '../features/economy/economy.css';
import './CharityPage.css';

function validDonationID(value: string): boolean {
  if (!/^[1-9][0-9]{0,18}$/.test(value)) return false;
  return BigInt(value) <= 9_223_372_036_854_775_807n;
}

function DonationDetailContent({ donationID }: { donationID: string }) {
  const { t } = useTranslation();
  const session = useUserSession();
  const accountID = session.data?.user.id;
  const location = useLocation();
  const listReturn = listReturnPath(location.state, '/charity');
  const returnTo = listReturn === '/charity' ? '/charity?tab=donations' : listReturn;
  const valid = validDonationID(donationID);
  const donation = useDonation(valid ? donationID : undefined, valid);
  return (
    <div className="page economy-page economy-charity-page">
      <PageHeader
        title={t('user.charity.donationDetailTitle')}
        description={t('user.charity.donationDetailDescription')}
        icon="charity"
        back={<Link to={returnTo}>{t('user.charity.backToDonations')}</Link>}
      />
      {!valid ? (
        <EmptyState
          title={t('user.charity.donationNotFound')}
          body={t('user.charity.donationNotFoundBody')}
          action={
            <Link className="btn btn-secondary" to={returnTo}>
              {t('user.charity.backToDonations')}
            </Link>
          }
        />
      ) : donation.isPending ? (
        <LoadingState />
      ) : donation.error && isNotFoundError(donation.error) ? (
        <EmptyState
          title={t('user.charity.donationNotFound')}
          body={t('user.charity.donationNotFoundBody')}
          action={
            <Link className="btn btn-secondary" to={returnTo}>
              {t('user.charity.backToDonations')}
            </Link>
          }
        />
      ) : donation.error ? (
        <ErrorState error={donation.error} onRetry={() => void donation.refetch()} />
      ) : (
        <>
          <CharitySafetyNotice />
          <DonationCard
            key={accountID}
            donation={donation.data}
            accountID={accountID}
            showDetailLink={false}
            disabled={donation.isFetching}
            keysContent={
              accountID ? (
                <OwnerDonationKeys
                  key={`${accountID}:${donationID}`}
                  accountID={accountID}
                  donationID={donationID}
                />
              ) : (
                <LoadingState />
              )
            }
          />
        </>
      )}
    </div>
  );
}

function CharityContent() {
  const { t, i18n } = useTranslation();
  const [searchParams, setSearchParams] = useSearchState();
  const tab =
    searchParams.get('tab') === 'donations'
      ? 'donations'
      : searchParams.get('tab') === 'donate'
        ? 'donate'
        : 'models';
  const selectTab = (value: string) => {
    setSearchParams((previous) => {
      const next = new URLSearchParams(previous);
      next.set('tab', value);
      return next;
    });
  };
  const session = useUserSession();
  const publicConfig = usePublicConfig();
  const capability = useCharityCapability();
  const accountID = session.data?.user.id;
  const intake = capability.data?.donationIntake;
  const configuredDonationNotice = (i18n.resolvedLanguage ?? i18n.language).startsWith('zh')
    ? publicConfig.data?.charityDonationNoticeZh
    : publicConfig.data?.charityDonationNoticeEn;

  return (
    <div className="page economy-page economy-charity-page">
      <PageHeader
        title={t('user.charity.title')}
        description={t('user.charity.description')}
        icon="charity"
        actions={
          <>
            <Link className="btn btn-secondary" to="/keys#client">
              {t('user.charity.apiAccess')}
            </Link>
            {session.data?.user.effective_level === 5 ||
            session.data?.user.effective_level === 6 ? (
              <Link className="btn btn-secondary" to="/steward?tab=charity">
                {t('user.charity.manageCharity')}
              </Link>
            ) : null}
          </>
        }
      />
      <Tabs
        label={t('user.charity.sections')}
        value={tab}
        onChange={selectTab}
        tabs={[
          { value: 'models', label: t('user.charity.tabs.models') },
          { value: 'donations', label: t('user.charity.tabs.donations') },
          { value: 'donate', label: t('user.charity.tabs.donate') },
        ]}
      />
      <section
        hidden={tab !== 'models'}
        className="charity-catalog-workspace"
        aria-label={t('user.charity.catalog.modelsList')}
      >
        <CharitySafetyNotice />
        <CharityCatalogPanel key={accountID ?? 'no-account'} accountID={accountID} />
      </section>
      <section hidden={tab !== 'donations'} aria-label={t('user.charity.donationsTitle')}>
        {session.data ? (
          <div className="nb-stats charity-donation-stats">
            <div className="nb-stat">
              <div className="nb-stat__label">{t('user.charity.presentation.donationCredit')}</div>
              <div className="nb-stat__value">
                <CreditAmount value={session.data.user.donation_credit} />
              </div>
            </div>
          </div>
        ) : null}
        {accountID ? (
          <OwnerDonationsPanel accountID={accountID} enabled={tab === 'donations'} />
        ) : (
          <LoadingState />
        )}
        <Leaderboard board="charity" enabled={tab === 'donations'} />
      </section>
      <section
        hidden={tab !== 'donate'}
        id="submit-donation"
        aria-label={t('user.charity.submitDonation')}
      >
        {capability.error ? (
          <ErrorState error={capability.error} onRetry={() => void capability.refetch()} />
        ) : intake && intake !== 'open' ? (
          <DonationIntakePanel state={intake} />
        ) : !intake ? (
          <LoadingState />
        ) : null}
        {intake === 'open' && accountID ? (
          <DonationComposer
            key={accountID}
            draftNamespace={accountID}
            notice={configuredDonationNotice}
            enabled={tab === 'donate' && !capability.error && !capability.isFetching}
          />
        ) : null}
      </section>
    </div>
  );
}

export function CharityPage() {
  const { donationId } = useParams<{ donationId?: string }>();
  return (
    <UserPageGate>
      {donationId === undefined ? (
        <CharityContent />
      ) : (
        <DonationDetailContent donationID={donationId} />
      )}
    </UserPageGate>
  );
}
