import type {
  RecurringLimitAlignment,
  RecurringLimitInterval,
  RecurringLimitMetric,
  RecurringLimitMode,
  RecurringLimitState,
} from '@shared/operations/recurringLimits';

export type RecurringLimitsLocale = 'en' | 'zh';

export interface RecurringLimitsCopy {
  title: string;
  description: string;
  helpTitle: string;
  readOnly: string;
  readOnlyRestricted: string;
  accessLost: string;
  charityOnly: string;
  privacyNote: string;
  ruleCount: (count: number) => string;
  noRules: string;
  addRule: string;
  maxRules: string;
  rule: (index: number) => string;
  ruleId: (id: string) => string;
  unsavedRule: string;
  mode: string;
  interval: string;
  alignment: string;
  timeZone: string;
  weekStartsOn: string;
  metric: string;
  limit: string;
  usage: string;
  used: string;
  reserved: string;
  remaining: string;
  period: string;
  nextTransition: string;
  browserTime: string;
  currentOffset: string;
  limitPlaceholder: string;
  selectZone: string;
  save: string;
  saving: string;
  saveSuccess: string;
  remove: string;
  moveUp: string;
  moveDown: string;
  collapse: string;
  expand: string;
  structuralChanges: string;
  structuralLimit: string;
  structuralOrder: string;
  structuralAdded: (index: number) => string;
  structuralDeleted: string;
  structuralReset: (index: number) => string;
  noChanges: string;
  invalidLimit: string;
  missingLimit: string;
  invalidTimeZone: string;
  timeZoneUnavailable: string;
  retryTimeZones: string;
  invalidCombination: string;
  conflictTitle: string;
  conflictBody: string;
  conflictAuthority: string;
  conflictDraft: string;
  unknownTitle: string;
  unknownBody: string;
  retryOriginal: string;
  retryAuthority: string;
  discardChanges: string;
  authorityUnavailable: string;
  useCurrent: string;
  keepDraft: string;
  noPeriod: string;
  state: Record<RecurringLimitState, string>;
  modeValue: Record<RecurringLimitMode, string>;
  intervalValue: Record<RecurringLimitInterval, string>;
  alignmentValue: Record<RecurringLimitAlignment, string>;
  metricValue: Record<RecurringLimitMetric, string>;
  weekValue: (day: number) => string;
  weekStartValue: (day: number | null) => string;
  waitingFirstSuccess: string;
  naturalPeriod: string;
  firstSuccessPeriod: string;
  slidingRecovery: string;
  slidingStart: string;
  deleteEffect: string;
  preserveUsage: string;
  browserZone: (zone: string) => string;
  zoneExample: (zone: string, offset: string, local: string) => string;
  periodValue: (business: string, browser: string) => string;
  transitionValue: (business: string, browser: string) => string;
  offsetUnavailable: string;
  authoritySyncTitle: string;
  authoritySyncPending: string;
  authoritySyncUnavailable: string;
  formatMetricValue: (value: string, metric: RecurringLimitMetric) => string;
}

const en: RecurringLimitsCopy = {
  title: 'Recurring charity limits',
  description:
    'These limits count charity usage and in-flight reservations. Actual consumption is recorded in full, even when it exceeds the reservation.',
  helpTitle: 'How these limits and access work',
  readOnly: 'Read-only view for this key owner.',
  readOnlyRestricted:
    'This donation key is terminal or expired; recurring limits can no longer be changed.',
  accessLost:
    'Your session no longer has access to these recurring limits. Sign in again to continue.',
  charityOnly:
    "Donation limits count charity calls only. Personal calls are not included and may cause actual usage of a shared key to exceed the site's charity totals. We recommend using separate keys for donations and personal use.",
  privacyNote:
    "Administrators and current level-5 users can manage charity settings. Owners can view their limits. Steward views hide other donors' private details; these pages never show complete keys.",
  ruleCount: (count) => `${count} of 16 rules`,
  noRules: 'No recurring rules are configured for this key.',
  addRule: 'Add recurring rule',
  maxRules: 'This key already has the maximum of 16 recurring rules.',
  rule: (index) => `Rule ${index}`,
  ruleId: (id) => `Saved rule ${id}`,
  unsavedRule: 'Unsaved new rule',
  mode: 'Mode',
  interval: 'Period',
  alignment: 'Starts at',
  timeZone: 'Time zone',
  weekStartsOn: 'Week starts on',
  metric: 'Measure',
  limit: 'Limit',
  usage: 'Current usage',
  used: 'Used',
  reserved: 'In flight',
  remaining: 'Remaining',
  period: 'Current period',
  nextTransition: 'Next reset or recovery',
  browserTime: 'Browser time',
  currentOffset: 'Current offset',
  limitPlaceholder: 'Required',
  selectZone: 'Search an IANA time zone',
  save: 'Save recurring limits',
  saving: 'Saving…',
  saveSuccess: 'Recurring limits saved.',
  remove: 'Delete rule',
  moveUp: 'Move up',
  moveDown: 'Move down',
  collapse: 'Collapse rule',
  expand: 'Expand rule',
  structuralChanges: 'What this save changes',
  structuralLimit: 'Changing a limit keeps recorded usage and in-flight reservations.',
  structuralOrder: 'Changing the display order keeps recorded usage and in-flight reservations.',
  structuralAdded: (index) => `Rule ${index} starts constraining calls sent after this save.`,
  structuralDeleted: `Deleting a rule stops that constraint for new sends. Existing usage and accounting remain.`,
  structuralReset: (index) =>
    `Rule ${index} changes structure and starts counting again from this save. Its lifetime total and accounting remain.`,
  noChanges: 'Make a change before saving.',
  invalidLimit:
    'Enter a canonical non-negative integer for calls or tokens, or a canonical credit amount with at most three decimal places.',
  missingLimit: 'A limit is required for every rule.',
  invalidTimeZone: 'Choose a supported IANA time zone from the registry.',
  timeZoneUnavailable: 'The time-zone registry is unavailable. Retry before saving.',
  retryTimeZones: 'Retry time-zone registry',
  invalidCombination: 'This mode, period, start rule, and week start combination is not valid.',
  conflictTitle: 'The rules changed while you were editing.',
  conflictBody:
    'Your draft is still here. The current server rules are shown for comparison; save again after reviewing the differences.',
  conflictAuthority: 'Current server rules',
  conflictDraft: 'Your saved draft',
  unknownTitle: 'The save result is unknown.',
  unknownBody:
    'The request may have been accepted, but its response was lost. Retry the exact same save to safely replay it.',
  retryOriginal: 'Retry the same save',
  retryAuthority: 'Reload current server rules',
  discardChanges: 'Discard changes',
  authorityUnavailable:
    'The latest server rules could not be loaded. Keep the draft and retry the comparison.',
  useCurrent: 'Use current server rules',
  keepDraft: 'Keep my draft',
  noPeriod: 'No current period is established.',
  state: {
    limited: 'Limited',
    waiting_first_success: 'Waiting for first successful charity call',
    available: 'Available',
  },
  modeValue: { reset: 'Reset', sliding: 'Sliding window' },
  intervalValue: {
    '1h': '1 hour',
    '5h': '5 hours',
    day: 'Day',
    week: 'Week',
    month: 'Month',
  },
  alignmentValue: { first_success: 'First successful call', calendar: 'Calendar boundary' },
  metricValue: { calls: 'Calls', tokens: 'Tokens', credits: 'Credits' },
  weekValue: (day) =>
    ['Monday', 'Tuesday', 'Wednesday', 'Thursday', 'Friday', 'Saturday', 'Sunday'][day - 1] ??
    `Day ${day}`,
  weekStartValue: (day) => (day === null ? 'not set' : en.weekValue(day)),
  waitingFirstSuccess:
    'Waiting for the first successful charity call. A dispatch or HTTP header does not start this period.',
  naturalPeriod:
    "The selected time zone defines this rule's business time. The browser-local time is shown for reference.",
  firstSuccessPeriod:
    'This period starts at the first successful charity call in the selected time zone. The browser-local time is shown for reference.',
  slidingRecovery:
    'Capacity returns as old usage leaves this sliding window. No fixed reset time is promised.',
  slidingStart: 'Rolling window; no fixed start',
  deleteEffect:
    'Deleting this rule stops its constraint for new sends; existing usage and accounting are retained.',
  preserveUsage:
    'Calls already sent finish under the rules captured when sent. This change applies to later dispatches.',
  browserZone: (zone) => `Browser time zone: ${zone}`,
  zoneExample: (zone, offset, local) =>
    `${zone} · current offset ${offset} · local example ${local}`,
  periodValue: (business, browser) => `${business} · ${browser}`,
  transitionValue: (business, browser) => `${business} · ${browser}`,
  offsetUnavailable: 'offset unavailable',
  authoritySyncTitle: 'Save recorded',
  authoritySyncPending:
    'Save succeeded. Confirming the latest server state before allowing another change.',
  authoritySyncUnavailable:
    'The save succeeded, but the latest server state could not be confirmed. The values shown remain the last confirmed snapshot until reload succeeds. Editing and another save are paused until the authority is reloaded.',
  formatMetricValue: (value, metric) =>
    `${value} ${metric === 'calls' ? 'calls' : metric === 'tokens' ? 'tokens' : 'credits'}`,
};

const zh: RecurringLimitsCopy = {
  title: '公益循环限量',
  description: '规则只统计公益用量和在途预留；实际消耗即使超过预留，也会如实记录。',
  helpTitle: '了解限量与管理权限',
  readOnly: '当前密钥所有者只能查看。',
  readOnlyRestricted: '此捐赠密钥已终态或已到期，不能再修改循环限量。',
  accessLost: '当前会话已无法访问这些循环限量，请重新登录后继续。',
  charityOnly:
    '本站的捐赠限量只统计公益调用，不包含自用调用。同一密钥同时用于自用时，实际消耗可能高于本站记录的公益用量，影响限额控制。建议将捐赠密钥与自用密钥分开使用。',
  privacyNote:
    '管理员和当前 5 级用户可管理公益配置，捐赠者可查看本人限量。协管视图隐藏他人的捐赠者资料；这些页面都不会显示完整密钥。',
  ruleCount: (count) => `${count}/16 条规则`,
  noRules: '此密钥尚未配置循环规则。',
  addRule: '添加循环规则',
  maxRules: '此密钥已达到 16 条循环规则上限。',
  rule: (index) => `规则 ${index}`,
  ruleId: (id) => `已保存规则 ${id}`,
  unsavedRule: '未保存的新规则',
  mode: '模式',
  interval: '周期',
  alignment: '起算方式',
  timeZone: '时区',
  weekStartsOn: '周起始日',
  metric: '计量维度',
  limit: '上限',
  usage: '当前用量',
  used: '已用',
  reserved: '在途预留',
  remaining: '剩余',
  period: '当前周期',
  nextTransition: '下次重置或恢复',
  browserTime: '浏览器时间',
  currentOffset: '当前偏移',
  limitPlaceholder: '必填',
  selectZone: '搜索 IANA 时区',
  save: '保存循环限量',
  saving: '保存中…',
  saveSuccess: '循环限量已保存。',
  remove: '删除规则',
  moveUp: '上移',
  moveDown: '下移',
  collapse: '收起规则',
  expand: '展开规则',
  structuralChanges: '本次保存的影响',
  structuralLimit: '调整上限会保留已记录用量和在途预留。',
  structuralOrder: '调整显示顺序会保留已记录用量和在途预留。',
  structuralAdded: (index) => `规则 ${index} 将从本次保存后发出的调用开始约束。`,
  structuralDeleted: '删除规则会停止它对新发送调用的约束；已有用量和账务保留。',
  structuralReset: (index) =>
    `规则 ${index} 发生结构变化，将从本次保存起重新计量；累计总量和账务保留。`,
  noChanges: '请先修改内容再保存。',
  invalidLimit: '调用次数或 Token 必须填写规范非负整数；积分必须是最多三位小数的规范金额。',
  missingLimit: '每条规则都必须填写上限。',
  invalidTimeZone: '请选择注册表支持的 IANA 时区。',
  timeZoneUnavailable: '时区注册表不可用，请重试后再保存。',
  retryTimeZones: '重试时区注册表',
  invalidCombination: '模式、周期、起算方式和周起始日的组合无效。',
  conflictTitle: '编辑期间规则已发生变化。',
  conflictBody: '你的草稿已保留。下面显示服务器当前规则供比较；检查差异后请再次明确保存。',
  conflictAuthority: '服务器当前规则',
  conflictDraft: '你的保存草稿',
  unknownTitle: '保存结果未知。',
  unknownBody: '请求可能已经接受，但响应丢失。请重试完全相同的保存，以安全重放。',
  retryOriginal: '重试相同保存',
  retryAuthority: '重新读取服务器当前规则',
  discardChanges: '取消修改',
  authorityUnavailable: '无法加载服务器最新规则。草稿已保留，请重试比较。',
  useCurrent: '使用服务器当前规则',
  keepDraft: '保留我的草稿',
  noPeriod: '尚未建立当前周期。',
  state: {
    limited: '受限',
    waiting_first_success: '等待首次成功公益调用',
    available: '可用',
  },
  modeValue: { reset: '刷新', sliding: '滑动窗口' },
  intervalValue: { '1h': '1 小时', '5h': '5 小时', day: '日', week: '周', month: '月' },
  alignmentValue: { first_success: '首次成功调用', calendar: '自然边界' },
  metricValue: { calls: '调用次数', tokens: 'Token 数', credits: '积分' },
  weekValue: (day) =>
    ['周一', '周二', '周三', '周四', '周五', '周六', '周日'][day - 1] ?? `第 ${day} 天`,
  weekStartValue: (day) => (day === null ? '未设置' : zh.weekValue(day)),
  waitingFirstSuccess: '等待首次成功公益调用；发送或 HTTP 头都不会启动此周期。',
  naturalPeriod: '所选时区决定此规则的业务时间；同时显示浏览器当地时间供参考。',
  firstSuccessPeriod: '此周期从所选时区内首次成功公益调用开始；同时显示浏览器当地时间供参考。',
  slidingRecovery: '旧用量移出滑动窗口后容量恢复，不承诺固定重置时间。',
  slidingStart: '滑动窗口；无固定起算',
  deleteEffect: '删除规则会停止它对新发送调用的约束；已有用量和账务保留。',
  preserveUsage: '已发出的调用按发送时捕获的规则收尾；此变更约束之后发送的调用。',
  browserZone: (zone) => `当前浏览器时区：${zone}`,
  zoneExample: (zone, offset, local) => `${zone} · 当前偏移 ${offset} · 当地示例 ${local}`,
  periodValue: (business, browser) => `${business} · ${browser}`,
  transitionValue: (business, browser) => `${business} · ${browser}`,
  offsetUnavailable: '无法显示偏移',
  authoritySyncTitle: '保存已记录',
  authoritySyncPending: '保存已成功，正在确认服务器最新状态；确认完成前不能再次修改。',
  authoritySyncUnavailable:
    '保存已成功，但暂时无法确认服务器最新状态。当前显示仍是上次确认的服务器快照，重新读取成功前会暂停编辑和再次保存。',
  formatMetricValue: (value, metric) =>
    `${value}${metric === 'calls' ? '次' : metric === 'tokens' ? ' Token' : '积分'}`,
};

export const recurringLimitsCopy: Record<RecurringLimitsLocale, RecurringLimitsCopy> = { en, zh };

export function copyForRecurringLimits(language?: string): RecurringLimitsCopy {
  return recurringLimitsCopy[language?.toLowerCase().startsWith('zh') ? 'zh' : 'en'];
}
