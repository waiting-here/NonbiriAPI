import { useTranslation } from 'react-i18next';
import { Link, useParams } from 'react-router';
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
        back={<Link to="/charity">{t('user.charity.backToDonations')}</Link>}
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
  const session = useUserSession();
  const publicConfig = usePublicConfig();
  const capability = useCharityCapability();
  const donations = useDonations();
  const accountID = session.data?.user.id;
  const intake = capability.data?.donationIntake;
  const choices = useEndpointKeyChoices(
    donations.data ?? [],
    intake === 'open' && donations.isSuccess,
  );
  const configuredDonationNotice = (i18n.resolvedLanguage ?? i18n.language).startsWith('zh')
    ? publicConfig.data?.charityDonationNoticeZh
    : publicConfig.data?.charityDonationNoticeEn;

  return (
    <div className="page economy-page economy-charity-page">
      <PageHeader
        eyebrow={t('user.charity.eyebrow')}
        title={t('user.charity.title')}
        description={t('user.charity.description')}
        icon="charity"
      />

      <section className="economy-capability-grid" aria-label={t('user.charity.capabilities')}>
        {capability.isPending ? (
          <LoadingState />
        ) : capability.error ? (
          <ErrorState error={capability.error} onRetry={() => void capability.refetch()} />
        ) : capability.data ? (
          <>
            <CharityCapabilityPanel capability={capability.data} />
            <DonationIntakePanel state={capability.data.donationIntake} />
          </>
        ) : (
          <LoadingState />
        )}
      </section>

      <CharitySafetyNotice />

      <section aria-labelledby="donations-title">
        <div className="card-title-row economy-section-heading">
          <div>
            <p className="eyebrow">{t('user.charity.yourResources')}</p>
            <h2 id="donations-title">{t('user.charity.donationsTitle')}</h2>
          </div>
        </div>
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
        ) : (
          <>
            <DonationKeyOverview donations={donations.data} />
            {donations.data.length === 0 ? (
              <EmptyState
                title={t('user.charity.noDonations')}
                body={t('user.charity.noDonationsBody')}
              />
            ) : (
              <div className="item-list economy-donation-list">
                {donations.data.map((donation) => (
                  <DonationCard key={donation.id} donation={donation} />
                ))}
              </div>
            )}
          </>
        )}
      </section>

      {intake === 'open' && donations.isSuccess && accountID ? (
        <section id="submit-donation" aria-labelledby="submit-donation-title">
          <h2 id="submit-donation-title" className="economy-visually-supported-title">
            {t('user.charity.submitDonation')}
          </h2>
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
        </section>
      ) : null}
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
