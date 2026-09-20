import { useTranslation } from 'react-i18next';

export function useLoanText() {
  const { i18n } = useTranslation();
  return (zh: string, en: string) =>
    (i18n.resolvedLanguage ?? i18n.language).startsWith('zh') ? zh : en;
}

export const loanReasons = [
  [
    '为了让还款更轻松，我们将替您提前完成本息扣款。',
    'To make repayment easier, we will deduct the principal and interest for you in advance.',
  ],
  [
    '为了免去反复操作的麻烦，放款与扣款将为您一站办妥。',
    'To save you repeated steps, disbursement and deduction are handled together.',
  ],
  [
    '为了让您专心实现财富目标，本息处理这种小事就交给我们。',
    'Focus on your financial goals and leave the little matter of principal and interest to us.',
  ],
  [
    '为了避免余额过高影响消费判断，我们将主动帮助您优化余额结构。',
    'To keep a high balance from clouding your spending decisions, we will help optimize your balances.',
  ],
  [
    '为了让每一笔后续收入都有明确用途，我们将提前为您规划还款目标。',
    'To give every future credit a purpose, we will plan your repayment target in advance.',
  ],
  [
    '为了节省您日后的宝贵时间，我们将省去等待您主动还款的步骤。',
    'To save your valuable time later, we will skip waiting for you to make a repayment.',
  ],
  [
    '为了把不确定的未来变成确定的现在，本次本息将一次性处理。',
    'To turn an uncertain future into a certain present, principal and interest are handled all at once.',
  ],
  [
    '为了帮您建立更有动力的奋斗目标，余额不足时也可先记为负数。',
    'To give you a more motivating goal, an insufficient balance can be recorded as negative.',
  ],
  [
    '为了避免您忘记还款，系统将主动完成扣款，差额由您从容补齐。',
    'To prevent a forgotten repayment, the system deducts it now; you can make up the shortfall at your leisure.',
  ],
  [
    '为了减少您对财务细节的顾虑，我们将替您妥善安排余额的去向。',
    'To spare you the financial details, we will carefully arrange where your balance goes.',
  ],
] as const;
