import { Button } from '@shared/components/ui/Button';
import { Icon } from '@shared/components/Icon';
import { formatCount } from '@shared/utils/formatNumber';
import { Panel, PanelHead, PanelBody } from '@shared/components/ui/Panel';
import { cards, rarities, tierOf } from './catalog';
import { poolOpen } from './rules';
import { CardTile } from './CardTile';
import type { Action, Card, Snapshot } from './types';

export function TicketIcon({ limited = false }: { limited?: boolean }) {
  return (
    <svg
      className={'gacha-ticket' + (limited ? ' gacha-ticket--limited' : '')}
      viewBox="0 0 64 48"
      fill="none"
      aria-hidden="true"
    >
      <path d="M5 10h54v9a5 5 0 0 0 0 10v9H5v-9a5 5 0 0 0 0-10z" fill="currentColor" />
      <path
        d="M13 15h38v18H13zM44 10v28"
        stroke="currentColor"
        className="gacha-ticket__line"
        strokeWidth="1.5"
      />
      <path
        d="m28 16 2.4 5.6L36 24l-5.6 2.4L28 32l-2.4-5.6L20 24l5.6-2.4z"
        className="gacha-ticket__star"
        fill="currentColor"
      />
    </svg>
  );
}
export function Shop({
  state,
  busy,
  english,
  act,
  inspect,
}: {
  state: Snapshot;
  busy: boolean;
  english: boolean;
  act: (action: Action, message: string) => void;
  inspect: (card: Card) => void;
}) {
  const t = (zh: string, en: string) => (english ? en : zh);
  const s = state.shop;
  const ticketOffers = [
    {
      id: 'ticket-n',
      rarity: 'N' as const,
      cost: s.n_ticket,
      quantity: 1,
      ticket: 'permanent' as const,
    },
    {
      id: 'ticket-r',
      rarity: 'R' as const,
      cost: s.r_ticket,
      quantity: 1,
      ticket: 'permanent' as const,
    },
    {
      id: 'ticket-sr',
      rarity: 'SR' as const,
      cost: s.sr_ticket,
      quantity: 1,
      ticket: 'limited' as const,
    },
    {
      id: 'ticket-ssr',
      rarity: 'SSR' as const,
      cost: s.ssr_tickets,
      quantity: 2,
      ticket: 'limited' as const,
    },
  ];
  const redeem = (product: string) =>
    act(
      { action: 'redeem', product, revision: s.revision },
      t('兑换成功，已放入你的收藏', 'Exchange complete'),
    );
  const limited = new Set(
    state.pools.filter((p) => poolOpen(p, state.server_time)).flatMap((p) => p.featured),
  );
  return (
    <div className="gacha-shop">
      <div className="gacha-section-heading">
        <div>
          <span className="gacha-eyebrow">THE MYSTERY EMPORIUM</span>
          <h2>{t('让重逢，也有新的惊喜', 'Turn familiar faces into new surprises')}</h2>
          <p>
            {t(
              '重复获得的卡牌，都会成为下一次相遇的碎片。',
              'Every duplicate brings your next encounter closer.',
            )}
          </p>
        </div>
      </div>
      <div className="gacha-fragments">
        {rarities.map((r) => (
          <div key={r} className={'gacha-fragment gacha-fragment--' + r}>
            <Icon name="models" />
            <span>
              {r} {t('卡牌碎片', 'shards')}
              <strong>{state.fragments[r].toLocaleString()}</strong>
            </span>
          </div>
        ))}
      </div>
      <Panel>
        <PanelHead
          title={t('再一次相遇', 'One more encounter')}
          description={t(
            '本月已兑：常驻券 ' +
              state.month_used.permanent +
              '/' +
              s.permanent_cap +
              ' · 限定券 ' +
              state.month_used.limited +
              '/' +
              s.limited_cap +
              '，不同碎片渠道合并计数。',
            'Monthly use: standard ' +
              state.month_used.permanent +
              '/' +
              s.permanent_cap +
              ' · limited ' +
              state.month_used.limited +
              '/' +
              s.limited_cap +
              '. Channels share the same limit.',
          )}
        />
        <PanelBody>
          <div className="gacha-offers">
            {ticketOffers.map((offer) => {
              const full =
                state.month_used[offer.ticket] + offer.quantity >
                (offer.ticket === 'permanent' ? s.permanent_cap : s.limited_cap);
              const insufficient = state.fragments[offer.rarity] < offer.cost;
              return (
                <div className="gacha-offer" key={offer.id}>
                  <TicketIcon limited={offer.ticket === 'limited'} />
                  <h3>
                    {offer.ticket === 'permanent'
                      ? t('常驻抽奖券', 'Standard ticket')
                      : t('限定抽奖券', 'Limited ticket')}{' '}
                    ×{offer.quantity}
                  </h3>
                  <p>
                    {offer.cost} {offer.rarity} {t('卡牌碎片', 'shards')}
                  </p>
                  <Button disabled={busy || full || insufficient} onClick={() => redeem(offer.id)}>
                    {full
                      ? t('本月额度不足', 'Monthly limit reached')
                      : insufficient
                        ? t('碎片不足', 'Need more shards')
                        : t('兑换', 'Exchange')}
                  </Button>
                </div>
              );
            })}
          </div>
        </PanelBody>
      </Panel>
      {[
        {
          source: 'shop',
          title: t('口袋应援团', 'Pocket cheer squad'),
          sub: t('常驻专属，无法从奖池抽取。', 'Always available, exclusively in the shop.'),
        },
        {
          source: 'limited',
          title: t('此刻限定', 'Here for a limited time'),
          sub: t(
            '奖池开放期间可兑换，每张卡牌限兑换一次。',
            'Available while its pool is open. Each card can be exchanged once.',
          ),
        },
      ].map((section) => (
        <section key={section.source} className="gacha-shop__cards">
          <h3>{section.title}</h3>
          <p>{section.sub}</p>
          <div className="gacha-catalog">
            {cards
              .filter(
                (card) =>
                  card.source === section.source &&
                  (card.source === 'shop' || limited.has(card.id)),
              )
              .map((card) => {
                const owned = !!state.owned[card.id],
                  cost =
                    card.rarity === 'SP'
                      ? s.sp_card
                      : tierOf(card.rarity) === 'SSR'
                        ? s.ssr_card
                        : s.sr_card;
                const insufficient =
                  card.source === 'shop'
                    ? state.fragments.N < s.chibi_n || state.fragments.R < s.chibi_r
                    : state.fragments[tierOf(card.rarity)] < cost;
                return (
                  <div className="gacha-shop-card" key={card.id}>
                    <CardTile
                      card={card}
                      english={english}
                      owned={owned}
                      onClick={() => inspect(card)}
                    />
                    <p className="gacha-shop-card__price">
                      {t('兑换需要：', 'Cost: ')}
                      {card.source === 'shop' ? (
                        <>
                          <span>
                            {formatCount(s.chibi_n).display} N {t('碎片', 'shards')}
                          </span>
                          {t(' 和 ', ' and ')}
                          <span>
                            {formatCount(s.chibi_r).display} R {t('碎片', 'shards')}
                          </span>
                        </>
                      ) : (
                        <span>
                          {formatCount(cost).display} {tierOf(card.rarity)} {t('碎片', 'shards')}
                        </span>
                      )}
                    </p>
                    <Button
                      block
                      disabled={busy || owned || insufficient}
                      onClick={() => redeem(card.id)}
                    >
                      {owned
                        ? t('已珍藏', 'Collected')
                        : insufficient
                          ? t('碎片不足', 'Need more shards')
                          : t('兑换收藏', 'Add to collection')}
                    </Button>
                  </div>
                );
              })}
          </div>
          {section.source === 'limited' && !limited.size && (
            <p>
              {t(
                '暂无开放的限定奖池。下一次相遇，值得期待。',
                'No limited pools are open. There is more to look forward to.',
              )}
            </p>
          )}
        </section>
      ))}
    </div>
  );
}
