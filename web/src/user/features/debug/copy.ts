export interface DebugCopy {
  eyebrow: string;
  title: string;
  description: string;
  warningTitle: string;
  warningBody: string;
  dry: string;
  live: string;
  dryBadge: string;
  liveBadge: string;
  actualToggle: string;
  actualEnabled: string;
  enableActual: string;
  start: string;
  replace: string;
  stop: string;
  starting: string;
  connecting: string;
  connected: string;
  reconnecting: string;
  stopped: string;
  replaced: string;
  expired: string;
  error: string;
  sessionInfo: string;
  sessionID: string;
  generation: string;
  created: string;
  expires: string;
  idleExpires: string;
  lastEvent: string;
  resourceBudget: string;
  noSession: string;
  noSessionBody: string;
  startHelp: string;
  externalClient: string;
  curlLabel: string;
  callerKeyPlaceholder: string;
  controls: string;
  search: string;
  searchPlaceholder: string;
  statusFilter: string;
  modeFilter: string;
  modelFilter: string;
  timeFilter: string;
  all: string;
  oneHour: string;
  day: string;
  pauseFollow: string;
  follow: string;
  clearView: string;
  requestList: string;
  requestCount: (count: number) => string;
  noRequests: string;
  noRequestsBody: string;
  details: string;
  structured: string;
  raw: string;
  summary: string;
  timeline: string;
  parameters: string;
  messages: string;
  tools: string;
  effective: string;
  response: string;
  copy: string;
  copied: string;
  copyFailed: string;
  field: string;
  type: string;
  source: string;
  changed: string;
  truncatedField: string;
  presence: string;
  callerValue: string;
  effectiveValue: string;
  absent: string;
  nullValue: string;
  falseValue: string;
  zero: string;
  emptyString: string;
  emptyArray: string;
  emptyObject: string;
  value: string;
  notEvaluated: string;
  hidden: string;
  dropped: (count: number) => string;
  gap: string;
  gapBody: string;
  truncated: string;
  incomplete: string;
  cancelled: string;
  statusReceived: string;
  statusValidated: string;
  statusRouting: string;
  statusDispatching: string;
  statusStreaming: string;
  statusCompleted: string;
  statusDryCompleted: string;
  statusFailed: string;
  statusCancelled: string;
  statusIncomplete: string;
  unknown: string;
  caller: string;
  policy: string;
  effectiveSuffix: string;
  callerJSON: string;
  applied: string;
  unchanged: string;
  confirmTitle: string;
  confirmBody: string;
  confirmEnable: string;
  sessionInfluence: string;
}

const en: DebugCopy = {
  eyebrow: 'User station',
  title: 'Request debugger',
  description:
    'Observe your own CallerKey requests in this browser tab. Captured data is temporary and stays in memory.',
  warningTitle: 'Safety notice',
  warningBody:
    'Starting a debug session changes your other clients to a dry run. Stop the session when you do not want their requests intercepted.',
  dry: 'Dry run',
  live: 'Actual sending',
  dryBadge: 'Dry run active',
  liveBadge: 'Actual sending enabled',
  actualToggle: 'Send subsequent requests to the upstream',
  actualEnabled: 'Actual sending is enabled',
  enableActual: 'Enable actual sending',
  start: 'Start debug session',
  replace: 'Replace session',
  stop: 'Stop and clear session',
  starting: 'Starting',
  connecting: 'Connecting',
  connected: 'Connected',
  reconnecting: 'Reconnecting · dry run',
  stopped: 'Stopped',
  replaced: 'Replaced',
  expired: 'Session ended',
  error: 'Debug operation failed',
  sessionInfo: 'Session metadata',
  sessionID: 'Session ID',
  generation: 'Generation',
  created: 'Created',
  expires: 'Expires',
  idleExpires: 'Idle expiry',
  lastEvent: 'Last event',
  resourceBudget: 'Public resource budget',
  noSession: 'No active debug session',
  noSessionBody: 'Start a session to observe requests made by your own external client.',
  startHelp:
    'Use the placeholder below in your own client. The page never asks you to paste a real CallerKey.',
  externalClient: 'External client example',
  curlLabel: 'Example request',
  callerKeyPlaceholder: '$NONBIRI_API_KEY',
  controls: 'View controls',
  search: 'Search',
  searchPlaceholder: 'Search request id or model',
  statusFilter: 'Status',
  modeFilter: 'Mode',
  modelFilter: 'Model',
  timeFilter: 'Time',
  all: 'All',
  oneHour: 'Last hour',
  day: 'Last day',
  pauseFollow: 'Pause follow',
  follow: 'Follow newest request',
  clearView: 'Clear browser view',
  requestList: 'Requests',
  requestCount: (count) => `${count} request${count === 1 ? '' : 's'}`,
  noRequests: 'No captured requests',
  noRequestsBody:
    'Requests from your external client will appear here while the session is connected.',
  details: 'Request details',
  structured: 'Structured',
  raw: 'Raw JSON',
  summary: 'Summary',
  timeline: 'Timeline',
  parameters: 'Request parameters',
  messages: 'Messages',
  tools: 'Tools',
  effective: 'Safe effective projection',
  response: 'Caller-visible response',
  copy: 'Copy section',
  copied: 'Copied',
  copyFailed: 'Copy failed',
  field: 'Field',
  type: 'Type',
  source: 'Source',
  changed: 'Changed',
  truncatedField: 'Truncated',
  presence: 'Presence',
  callerValue: 'Caller value',
  effectiveValue: 'Effective value',
  absent: 'Absent',
  nullValue: 'null',
  falseValue: 'false',
  zero: '0',
  emptyString: 'empty string',
  emptyArray: 'empty array',
  emptyObject: 'empty object',
  value: 'Value',
  notEvaluated: 'Not selected · not evaluated',
  hidden: 'Applied · value hidden',
  dropped: (count) => `${count} debug copies dropped`,
  gap: 'Recovery gap',
  gapBody: 'Some events were no longer retained. The next safe snapshot will replace this view.',
  truncated: 'Truncated',
  incomplete: 'Incomplete',
  cancelled: 'Cancelled',
  statusReceived: 'Received',
  statusValidated: 'Validated',
  statusRouting: 'Routing',
  statusDispatching: 'Dispatching',
  statusStreaming: 'Streaming',
  statusCompleted: 'Completed',
  statusDryCompleted: 'Dry run completed',
  statusFailed: 'Failed',
  statusCancelled: 'Cancelled',
  statusIncomplete: 'Incomplete',
  unknown: 'Unknown',
  caller: 'Caller',
  policy: 'Policy',
  effectiveSuffix: 'effective',
  callerJSON: 'Caller JSON',
  applied: 'Applied',
  unchanged: 'Unchanged',
  confirmTitle: 'Enable actual upstream sending?',
  confirmBody:
    'This sends subsequent requests to an upstream service. It can consume upstream quota and may use charity billing. You must confirm every time you enable it.',
  confirmEnable: 'Enable actual sending',
  sessionInfluence: 'This affects requests from your other clients while the session is active.',
};

const zh: DebugCopy = {
  eyebrow: '用户站',
  title: '请求调试器',
  description: '在此浏览器标签页观察你自己的 CallerKey 请求。捕获内容只临时保存在内存中。',
  warningTitle: '安全提示',
  warningBody: '启动调试会话会让你的其他客户端进入 Dry run。不要拦截时请停止会话。',
  dry: 'Dry run',
  live: '实际发送',
  dryBadge: 'Dry run 已启用',
  liveBadge: '实际发送已启用',
  actualToggle: '将后续请求发送到上游',
  actualEnabled: '实际发送已启用',
  enableActual: '启用实际发送',
  start: '启动调试会话',
  replace: '替换会话',
  stop: '停止并清空会话',
  starting: '启动中',
  connecting: '连接中',
  connected: '已连接',
  reconnecting: '重连中 · Dry run',
  stopped: '已停止',
  replaced: '已替换',
  expired: '会话已结束',
  error: '调试操作失败',
  sessionInfo: '会话元数据',
  sessionID: '会话 ID',
  generation: '代数',
  created: '创建时间',
  expires: '到期时间',
  idleExpires: '空闲到期',
  lastEvent: '最后事件',
  resourceBudget: '公开资源预算',
  noSession: '没有活动调试会话',
  noSessionBody: '启动会话后，可观察你自己的外部客户端请求。',
  startHelp: '在你自己的客户端中使用下方占位符。页面不会要求粘贴真实 CallerKey。',
  externalClient: '外部客户端示例',
  curlLabel: '请求示例',
  callerKeyPlaceholder: '$NONBIRI_API_KEY',
  controls: '视图控制',
  search: '搜索',
  searchPlaceholder: '搜索请求 ID 或模型',
  statusFilter: '状态',
  modeFilter: '模式',
  modelFilter: '模型',
  timeFilter: '时间',
  all: '全部',
  oneHour: '最近一小时',
  day: '最近一天',
  pauseFollow: '暂停跟随',
  follow: '跟随最新请求',
  clearView: '清空浏览器视图',
  requestList: '请求',
  requestCount: (count) => `${count} 个请求`,
  noRequests: '暂无捕获请求',
  noRequestsBody: '外部客户端请求会在连接保持时显示在这里。',
  details: '请求详情',
  structured: '结构化',
  raw: '原始 JSON',
  summary: '摘要',
  timeline: '时间线',
  parameters: '请求参数',
  messages: '消息',
  tools: '工具',
  effective: '安全有效投影',
  response: '调用方可见响应',
  copy: '复制区块',
  copied: '已复制',
  copyFailed: '复制失败',
  field: '字段',
  type: '类型',
  source: '来源',
  changed: '已变化',
  truncatedField: '已截断',
  presence: '存在形式',
  callerValue: '调用方值',
  effectiveValue: '有效值',
  absent: '缺省',
  nullValue: 'null',
  falseValue: 'false',
  zero: '0',
  emptyString: '空字符串',
  emptyArray: '空数组',
  emptyObject: '空对象',
  value: '值',
  notEvaluated: '未选择候选 · 未评估',
  hidden: '已应用 · 值已隐藏',
  dropped: (count) => `已丢弃 ${count} 个调试副本`,
  gap: '恢复缺口',
  gapBody: '部分事件已不再保留。下一次安全快照会替换当前视图。',
  truncated: '已截断',
  incomplete: '不完整',
  cancelled: '已取消',
  statusReceived: '已接收',
  statusValidated: '已校验',
  statusRouting: '路由中',
  statusDispatching: '派发中',
  statusStreaming: '流式传输中',
  statusCompleted: '已完成',
  statusDryCompleted: 'Dry run 已完成',
  statusFailed: '失败',
  statusCancelled: '已取消',
  statusIncomplete: '不完整',
  unknown: '未知',
  caller: '调用方',
  policy: '策略',
  effectiveSuffix: '有效值',
  callerJSON: '调用方 JSON',
  applied: '已应用',
  unchanged: '未变化',
  confirmTitle: '启用实际发送到上游？',
  confirmBody: '后续请求会发送到上游服务，可能消耗上游额度并触发公益账务。每次启用都必须重新确认。',
  confirmEnable: '启用实际发送',
  sessionInfluence: '会话活动期间，你的其他客户端请求也会受到影响。',
};

export function debugCopyForLanguage(language: string): DebugCopy {
  return language.toLowerCase().startsWith('zh') ? zh : en;
}
