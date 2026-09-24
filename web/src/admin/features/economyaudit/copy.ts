import { useTranslation } from 'react-i18next';
import type { Asset } from './api';
export function useEconomyText() {
  const { i18n } = useTranslation();
  return (zh: string, en: string) => (i18n.resolvedLanguage?.startsWith('zh') ? zh : en);
}
export type Text = ReturnType<typeof useEconomyText>;
export function assetLabel(asset: Asset, t: Text) {
  return {
    general: t('通用悠哉积分', 'General credits'),
    game: t('游戏悠哉积分', 'Game credits'),
    sketch_paper: t('草稿纸', 'Sketch paper'),
    sketch_brush: t('画笔', 'Paint brushes'),
  }[asset];
}
export function channelLabel(channel: string, t: Text) {
  return (
    (
      {
        admin: t('管理员调整', 'Administrator adjustments'),
        account: t('账号删除', 'Account deletion'),
        checkin: t('签到', 'Check-in'),
        welfare: t('每日福利', 'Daily welfare'),
        thursday: t('疯狂星期四', 'Thursday event'),
        api: t('自用 API', 'Personal API'),
        charity: t('公益 API', 'Charity API'),
        donation: t('捐赠回馈', 'Donation rewards'),
        fishing: t('池塘垂钓', 'Fishing'),
        linklink: t('连连看', 'Link-link'),
        rps: t('石头剪刀布', 'Rock paper scissors'),
        bidding: t('竞标对决', 'Bidding duel'),
        likes: t('回合制对战', 'Turn-based battle'),
        blackjack: t('二十一点', 'Blackjack'),
        onboarding: t('新人奖励', 'Onboarding rewards'),
        loan: t('赛博网贷', 'Credit exchange loan'),
        picture_book: t('喵帕斯的绘本', 'Picture book'),
        inactivity: t('低活跃衰减', 'Inactivity decay'),
        penalty: t('违规扣减', 'Penalties'),
        unclassified: t('未分类', 'Unclassified'),
      } as Record<string, string>
    )[channel] ?? channel
  );
}
