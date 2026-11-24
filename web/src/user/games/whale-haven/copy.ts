import { useTranslation } from 'react-i18next';
import { costWithCoupon, type Profile } from './api';

export function useWhaleText() {
  const { i18n } = useTranslation();
  const english = (i18n.resolvedLanguage ?? i18n.language).startsWith('en');
  return (zh: string, en: string) => (english ? en : zh);
}

export function unlockPriceText(
  profile: Profile,
  basePrice: number,
  t: ReturnType<typeof useWhaleText>,
) {
  const price = costWithCoupon(profile, basePrice);
  const total = t(
    `实付 ${price} 枚小鲸币`,
    `Total ${price} whale ${price === 1 ? 'coin' : 'coins'}`,
  );
  if (price < basePrice) {
    const saved = basePrice - price;
    return t(
      `原价 ${basePrice} 枚 · 1 张裁缝券抵 ${saved} 枚 · ${total}`,
      `List price ${basePrice} · One tailor coupon saves ${saved} · ${total}`,
    );
  }
  if (profile.snapshot.home.buffs?.wardrobeDiscountCoupons)
    return t(`${total} · 裁缝券保留`, `${total} · Tailor coupon kept`);
  return total;
}

export function guestVisitLabel(
  guest: Pick<Profile['social']['guests'][number], 'working' | 'allow_hire' | 'hire_blocked'>,
  t: ReturnType<typeof useWhaleText>,
) {
  if (guest.working) return t('正在帮工', 'Working');
  if (guest.hire_blocked === 'quota')
    return t('今日社交鲸币已满', 'Daily social coin limit reached');
  if (guest.hire_blocked === 'wallet_full') return t('暂时无法收取工资', 'Unable to receive wages');
  return guest.allow_hire ? t('愿意帮工', 'Available to hire') : t('只来串门', 'Visiting only');
}
