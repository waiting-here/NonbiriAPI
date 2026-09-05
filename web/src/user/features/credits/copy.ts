import { useTranslation } from 'react-i18next';
import type { HistoryEntry, HistoryKind } from './data';

const en = {
  title: 'Nonbiri credit history',
  description: 'View your credit changes and the requests behind them.',
  balance: 'Current available balance',
  category: 'Reason',
  direction: 'Money in / out',
  all: 'All',
  income: 'Credits in',
  expense: 'Credits out',
  from: 'From',
  to: 'Before',
  apply: 'Apply filters',
  reset: 'Clear filters',
  refresh: 'Refresh',
  localTime: 'Times use your device time zone.',
  invalidRange: 'Enter a valid time range, with the end after the start.',
  time: 'Time',
  change: 'Change',
  request: 'Related request',
  openRequest: 'View request',
  noRequest: '—',
  empty: 'No credit changes found',
  emptyBody: 'Try another filter, or check back after earning or spending credits.',
  pageSize: 'Rows per page',
  first: 'First',
  previous: 'Previous',
  next: 'Next',
  last: 'Last',
  page: 'Page',
  of: 'of',
  jump: 'Go',
  jumpLabel: 'Go to page',
  invalidPage: 'Enter a page number within the available range.',
  records: 'records',
  note: 'Funds set aside for calls and games appear as credits out. Unused funds return as a separate entry.',
  checkin: 'Check-in',
  welfare: 'Welfare pool',
  thursday: 'Thursday pool',
  fishing: 'Pond fishing',
  linklink: 'LinkLink',
  rps: 'Rock Paper Scissors',
  api: 'Own API calls',
  charity: 'Charity calls',
  donation: 'Donation rewards',
  admin: 'Administrator adjustments',
  penalty: 'Short-request penalties',
};
const zh: typeof en = {
  title: '悠哉积分流水',
  description: '查看积分的每次变化，以及相关的请求记录。',
  balance: '当前可用积分',
  category: '变化原因',
  direction: '收支方向',
  all: '全部',
  income: '收入',
  expense: '支出',
  from: '开始时间',
  to: '结束时间（不含）',
  apply: '筛选',
  reset: '清除筛选',
  refresh: '刷新',
  localTime: '时间按当前设备时区显示。',
  invalidRange: '请填写有效时间，结束时间应晚于开始时间。',
  time: '时间',
  change: '积分变化',
  request: '相关请求',
  openRequest: '查看请求',
  noRequest: '—',
  empty: '暂无符合条件的积分流水',
  emptyBody: '可以调整筛选条件，或在获得、使用积分后查看。',
  pageSize: '每页条数',
  first: '首页',
  previous: '上一页',
  next: '下一页',
  last: '末页',
  page: '第',
  of: '/',
  jump: '跳转',
  jumpLabel: '跳转页码',
  invalidPage: '请输入有效范围内的页码。',
  records: '条记录',
  note: '调用和游戏预留的积分会先记为支出，未使用的部分随后记为退还。',
  checkin: '签到',
  welfare: '低保池',
  thursday: '疯狂星期四',
  fishing: '池塘垂钓',
  linklink: '连连看',
  rps: '三人猜拳',
  api: '自有 API 调用',
  charity: '公益调用',
  donation: '捐赠回馈',
  admin: '管理员调整',
  penalty: '过短请求惩罚',
};
const reasons: Record<HistoryKind, [string, string]> = {
  admin_user_adjustment: ['管理员调整', 'Administrator adjustment'],
  admin_pool_adjustment: ['资金池调整', 'Pool adjustment'],
  account_delete_zero: ['账号余额结清', 'Account balance cleared'],
  checkin_award: ['签到奖励', 'Check-in reward'],
  anti_abuse_penalty: ['过短请求惩罚', 'Short-request penalty'],
  welfare_claim: ['领取低保', 'Welfare claimed'],
  thursday_contribution: ['疯狂星期四：投入', 'Thursday contribution'],
  thursday_payout: ['疯狂星期四：分配', 'Thursday payout'],
  thursday_finalize: ['疯狂星期四：结算', 'Thursday settlement'],
  forward_reserve: ['API 调用：预留', 'API call: funds reserved'],
  forward_settle: ['API 调用：补扣', 'API call: additional charge'],
  forward_release: ['API 调用：退还预留', 'API call: reservation returned'],
  charity_reserve: ['公益调用：预留', 'Charity call: funds reserved'],
  charity_settle: ['公益调用：补扣', 'Charity call: additional charge'],
  charity_release: ['公益调用：退还预留', 'Charity call: reservation returned'],
  donor_reward: ['捐赠回馈', 'Donation reward'],
  fishing_reserve: ['垂钓：鱼饵费用', 'Fishing: bait cost'],
  fishing_settle: ['垂钓：收获奖励', 'Fishing: catch reward'],
  fishing_release: ['垂钓：费用退还', 'Fishing: refund'],
  linklink_entry: ['连连看：入场费用', 'LinkLink: entry cost'],
  rps_queue_reserve: ['猜拳：入场资金预留', 'RPS: entry funds reserved'],
  rps_queue_release: ['猜拳：排队资金退还', 'RPS: queue funds returned'],
  rps_session_start: ['猜拳：对局资金调整', 'RPS: match funds adjusted'],
  rps_round_cut: ['猜拳：本轮结算', 'RPS: round settlement'],
  rps_terminal: ['猜拳：对局结算', 'RPS: match settlement'],
};
export function useCreditCopy() {
  const { i18n } = useTranslation();
  const chinese = !i18n.language.startsWith('en');
  return {
    copy: chinese ? zh : en,
    reason: (entry: HistoryEntry) => {
      if (
        (entry.kind === 'charity_settle' || entry.kind === 'forward_settle') &&
        !entry.delta.startsWith('-')
      ) {
        return entry.kind === 'charity_settle'
          ? chinese
            ? '公益调用：退还差额'
            : 'Charity call: unused funds returned'
          : chinese
            ? 'API 调用：退还差额'
            : 'API call: unused funds returned';
      }
      return reasons[entry.kind][chinese ? 0 : 1];
    },
  };
}
