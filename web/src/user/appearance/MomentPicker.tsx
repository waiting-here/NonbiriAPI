import { useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Button } from '@shared/components/ui/Button';
import { Field } from '@shared/components/ui/Field';
import { FilterBar } from '@shared/components/ui/FilterBar';
import { Panel, PanelHead, PanelBody, PanelFoot } from '@shared/components/ui/Panel';
import { Tabs } from '@shared/components/ui/Tabs';
import { Note } from '@shared/components/ui/Note';
import { ErrorState, LoadingState, EmptyState } from '@shared/components/States';
import { formatCount } from '@shared/utils/formatNumber';
import { appearances, useOptionalAppearance } from './context';
import { defaultMoment, momentArtworks, momentByKey, momentArtworkKeys } from './moments';
import { ArtworkImage, ArtworkBadge, ArtworkUnavailable } from '@shared/artwork/ArtworkImage';
import { useArtworkPolicy } from '@shared/artwork/context';
import './moments.css';

const kindKeys: Record<string, string> = {
  all: 'user.appearance.moments.all',
  cover: 'user.appearance.moments.cover',
  card: 'user.appearance.moments.card',
  illustration: 'user.appearance.moments.illustration',
};

export function MomentPicker() {
  const appearance = useOptionalAppearance();
  const policy = useArtworkPolicy();
  const { t, i18n } = useTranslation();
  const english = i18n.language.startsWith('en');
  const [choiceOverride, setChoice] = useState<string>();
  const choice = choiceOverride ?? appearance?.state.moment ?? defaultMoment.key;
  const [kind, setKind] = useState('all');
  const [search, setSearch] = useState('');
  const [searchDraft, setSearchDraft] = useState('');
  const [error, setError] = useState<unknown>(null);
  const refresh = appearance?.refresh;
  useEffect(() => {
    refresh?.();
  }, [refresh]);
  if (!appearance) return null;
  const options = momentArtworks.filter((item) =>
    (appearance.state.moments ?? [defaultMoment.key]).includes(item.key),
  );
  const current = momentByKey.get(choice) ?? defaultMoment;
  const matchingAppearance = appearances.find(
    (item) => 'card' in item && item.card === current.card?.id,
  );
  const available =
    options.some((item) => item.key === choice) && policy.allowed(momentArtworkKeys(current));
  const selected =
    appearance.state.selected === 'heart-moment' && appearance.state.moment === choice;
  const name = (item: typeof current) => (english ? item.nameEN : item.name);
  const filtered = options.filter(
    (item) =>
      (kind === 'all' || item.kind === kind) &&
      (item.name + item.nameEN + (item.card?.character ?? ''))
        .toLowerCase()
        .includes(search.toLowerCase()),
  );
  return (
    <Panel className="nb-moment-picker">
      <PanelHead
        title={t('user.appearance.moments.title')}
        description={t('user.appearance.moments.description')}
      />
      <PanelBody>
        {appearance.pending && <LoadingState />}
        {appearance.error ? (
          <ErrorState error={appearance.error} onRetry={appearance.refresh} />
        ) : null}
        {error ? <ErrorState error={error} /> : null}
        <div className="nb-moment-picker__selection">
          <ArtworkImage
            artworkKey={momentArtworkKeys(current)}
            src={current.art}
            alt={name(current)}
            className="nb-moment-picker__preview"
          />
          <div className="nb-moment-picker__copy">
            <strong>{name(current)}</strong>
            <ArtworkBadge artworkKey={momentArtworkKeys(current)} />
            <ArtworkUnavailable artworkKey={momentArtworkKeys(current)} />
            <span>{t(kindKeys[current.kind])}</span>
            {current.card?.rarity === 'SP' &&
              matchingAppearance &&
              policy.allowed('appearance:' + matchingAppearance.id) && (
                <small>{t('user.appearance.moments.spHint', { name: name(current) })}</small>
              )}
            <Button
              variant="primary"
              disabled={
                !available ||
                appearance.saving ||
                appearance.pending ||
                !!appearance.error ||
                selected
              }
              onClick={() => {
                setError(null);
                void appearance.select('heart-moment', choice).catch(setError);
              }}
            >
              {t(selected ? 'user.appearance.moments.applied' : 'user.appearance.moments.apply')}
            </Button>
          </div>
        </div>
        <Tabs
          label={t('user.appearance.moments.filter')}
          value={kind}
          onChange={setKind}
          tabs={['all', 'cover', 'card', 'illustration'].map((value) => ({
            value,
            label: t(kindKeys[value]),
          }))}
        />
        <FilterBar
          activeCount={search ? 1 : 0}
          ariaLabel={t('user.appearance.moments.search')}
          onSubmit={() => setSearch(searchDraft.trim())}
          onReset={() => {
            setSearch('');
            setSearchDraft('');
            setKind('all');
          }}
          search={
            <Field label={t('user.appearance.moments.search')}>
              {(props) => (
                <input
                  {...props}
                  value={searchDraft}
                  placeholder={t('user.appearance.moments.searchHint')}
                  onChange={(event) => setSearchDraft(event.target.value)}
                />
              )}
            </Field>
          }
        />
        <div
          className="nb-moment-picker__grid"
          role="radiogroup"
          aria-label={t('user.appearance.moments.choose')}
        >
          {filtered.map((item) => (
            <label
              key={item.key}
              className="nb-moment-option"
              data-selected={choice === item.key}
              data-unavailable={!policy.allowed(momentArtworkKeys(item))}
            >
              <input
                type="radio"
                name="heart-moment"
                value={item.key}
                checked={choice === item.key}
                disabled={appearance.saving || !policy.allowed(momentArtworkKeys(item))}
                onChange={() => {
                  setChoice(item.key);
                  setError(null);
                }}
              />
              <ArtworkImage
                artworkKey={momentArtworkKeys(item)}
                src={item.thumb}
                alt=""
                loading="lazy"
              />
              <ArtworkBadge artworkKey={momentArtworkKeys(item)} />
              <ArtworkUnavailable artworkKey={momentArtworkKeys(item)} />
              <span>{name(item)}</span>
              <small>{item.card?.rarity ?? t(kindKeys[item.kind])}</small>
            </label>
          ))}
        </div>
        {!filtered.length && !appearance.pending && (
          <EmptyState
            title={t('user.appearance.moments.empty')}
            body={t('user.appearance.moments.emptyHint')}
          />
        )}
      </PanelBody>
      <PanelFoot>
        <Note>
          {t('user.appearance.moments.availableCount', {
            countText: formatCount(
              options.filter((item) => policy.allowed(momentArtworkKeys(item))).length,
            ).display,
          })}
        </Note>
      </PanelFoot>
    </Panel>
  );
}
