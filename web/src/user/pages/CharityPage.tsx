import { useTranslation } from 'react-i18next';
import { Link, useParams, useSearchParams } from 'react-router';
import { EmptyState, ErrorState, LoadingState, PageHeader } from '@shared/components/States';
import { isNotFoundError } from '@shared/query/http';
import { usePublicConfig } from '@shared/query/publicConfig';
import { UserPageGate } from '../components/UserPageGate';
import { useUserSession } from '../data';
import {
  CharityCapabilityPanel,
  CharitySafetyNotice,
  DonationCard,
  DonationComposer,
  DonationKeyOverview,
  DonationOverviewPartialError,
  DonationIntakePanel,
} from '../features/economy/CharityPanels';
import { isDonationCollectionIncomplete } from '../features/economy/api';
import {
  useCharityCapability,
  useDonation,
  useDonations,
  useEndpointKeyChoices,
} from '../features/economy/queries';
import '../features/economy/economy.css';

function validDonationID(value: string): boolean {
  if (!/^[1-9][0-9]{0,18}$/.test(value)) return false;
  return BigInt(value) <= 9_223_372_036_854_775_807n;
}

function DonationDetailContent({ donationID }: { donationID: string }) {
  const { t } = useTranslation();
  const valid = validDonationID(donationID);
  const donation = useDonation(valid ? donationID : undefined, valid);
  return (
    <div className="page economy-page economy-charity-page">
      <PageHeader
        eyebrow={t('user.charity.eyebrow')}
        title={t('user.charity.donationDetailTitle')}
        description={t('user.charity.donationDetailDescription')}
        icon="charity"
        back={<Link to="/charity?tab=donations">{t('user.charity.backToDonations')}</Link>}
      />
      {!valid ? (
        <EmptyState
          title={t('user.charity.donationNotFound')}
          body={t('user.charity.donationNotFoundBody')}
          action={
            <Link className="btn btn-secondary" to="/charity">
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
            <Link className="btn btn-secondary" to="/charity">
              {t('user.charity.backToDonations')}
            </Link>
          }
        />
      ) : donation.error ? (
        <ErrorState error={donation.error} onRetry={() => void donation.refetch()} />
      ) : (
        <>
          <CharitySafetyNotice />
          <DonationCard donation={donation.data} showDetailLink={false} />
        </>
      )}
    </div>
  );
}

function CharityContent() {
  const { t, i18n } = useTranslation();
  const [searchParams, setSearchParams] = useSearchParams();
  const tab =
    searchParams.get('tab') === 'donations'
      ? 'donations'
      : searchParams.get('tab') === 'donate'
        ? 'donate'
        : 'models';
  const selectTab = (value: string) => {
    const next = new URLSearchParams(searchParams);
    next.set('tab', value);
    setSearchParams(next);
  };
  const session = useUserSession();
  const publicConfig = usePublicConfig();
  const capability = useCharityCapability();
  const donations = useDonations();
  const accountID = session.data?.user.id;
  const intake = capability.data?.donationIntake;
  const choices = useEndpointKeyChoices(
    donations.data ?? [],
    tab === 'donate' && intake === 'open' && donations.isSuccess,
  );
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
            <Link className="btn btn-secondary" to="/keys">
              {t('user.charity.apiAccess')}
            </Link>
            {session.data?.user.effective_level === 5 ? (
              <Link className="btn btn-secondary" to="/steward?tab=charity">
                {t('user.charity.manageCharity')}
              </Link>
            ) : null}
          </>
        }
      />
      <nav className="economy-tabs" role="tablist" aria-label={t('user.charity.sections')}>
        {(
          [
            ['models', 'user.charity.tabs.models'],
            ['donations', 'user.charity.tabs.donations'],
            ['donate', 'user.charity.tabs.donate'],
          ] as const
        ).map(([value, label]) => (
          <button
            key={value}
            type="button"
            role="tab"
            aria-selected={tab === value}
            className={`btn btn-quiet${tab === value ? ' is-active' : ''}`}
            onClick={() => selectTab(value)}
          >
            {t(label)}
          </button>
        ))}
      </nav>
      <section
        hidden={tab !== 'models'}
        className="economy-model-workspace"
        aria-label={t('user.charity.availableModels')}
      >
        {capability.isPending ? (
          <LoadingState />
        ) : capability.error ? (
          <ErrorState error={capability.error} onRetry={() => void capability.refetch()} />
        ) : capability.data ? (
          <CharityCapabilityPanel capability={capability.data} />
        ) : (
          <LoadingState />
        )}
        <CharitySafetyNotice />
      </section>
      <section hidden={tab !== 'donations'} aria-label={t('user.charity.donationsTitle')}>
        {donations.isPending ? (
          <LoadingState />
        ) : donations.error ? (
          isDonationCollectionIncomplete(donations.error) ? (
            <DonationOverviewPartialError onRetry={() => void donations.refetch()} />
          ) : (
            <ErrorState error={donations.error} onRetry={() => void donations.refetch()} />
          )
        ) : !donations.data ? (
          <LoadingState />
        ) : donations.data.length === 0 ? (
          <EmptyState
            title={t('user.charity.noDonations')}
            body={t('user.charity.noDonationsBody')}
            action={
              intake === 'open' ? (
                <button
                  className="btn btn-primary"
                  type="button"
                  onClick={() => selectTab('donate')}
                >
                  {t('user.charity.submitDonation')}
                </button>
              ) : undefined
            }
          />
        ) : (
          <DonationKeyOverview donations={donations.data} />
        )}
      </section>
      <section
        hidden={tab !== 'donate'}
        id="submit-donation"
        aria-label={t('user.charity.submitDonation')}
      >
        {capability.error ? (
          <ErrorState error={capability.error} onRetry={() => void capability.refetch()} />
        ) : intake ? (
          <DonationIntakePanel state={intake} />
        ) : (
          <LoadingState />
        )}
        {intake === 'open' && donations.isSuccess && accountID ? (
          <>
            {choices.isPending ? (
              <LoadingState />
            ) : choices.error ? (
              <ErrorState error={choices.error} onRetry={() => void choices.refetch()} />
            ) : (
              <DonationComposer
                key={accountID}
                choices={choices.data}
                draftNamespace={accountID}
                notice={configuredDonationNotice}
              />
            )}
          </>
        ) : donations.error ? (
          <ErrorState error={donations.error} onRetry={() => void donations.refetch()} />
        ) : donations.isPending ? (
          <LoadingState />
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
