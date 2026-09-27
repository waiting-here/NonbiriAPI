export const accountCopy = {
  en: { deleted: 'Deleted account', allAccounts: 'All accounts', activeAccounts: 'Current accounts', deletedAccounts: 'Deleted accounts', discordInvalid: 'Enter a valid Discord ID.', close: 'Close', unknown: 'Unknown', formerID: 'Former user ID', discord: 'Discord ID', registered: 'Registered', deletedAt: 'Deleted', level: 'Level at deletion', source: 'Deletion source', ban: 'Ban at deletion', pause: 'Charity pause at deletion', active: 'Active', inactive: 'Inactive', until: 'Until', reason: 'Reason', blacklist: 'Blacklist action', general: 'General balance', game: 'Game balance', donation: 'Donation credit', paper: 'Sketch Paper', brush: 'Sketch Brush', sameIdentity: 'All accounts for this Discord ID', alert: 'Related alert', aborts: 'Self-deletion match cancellations (last 90 days)', noAborts: 'No retained match cancellations', previous: 'Previous', next: 'Next', match: 'Match', occurred: 'Occurred' },
  zh: { deleted: '已删除账号', allAccounts: '全部账号', activeAccounts: '当前账号', deletedAccounts: '历史账号', discordInvalid: '请填写有效的 Discord ID。', close: '关闭', unknown: '未知', formerID: '原站内 ID', discord: 'Discord ID', registered: '注册时间', deletedAt: '删除时间', level: '删除时等级', source: '删除来源', ban: '删除时封禁', pause: '删除时公益暂停', active: '生效中', inactive: '未生效', until: '截至', reason: '原因', blacklist: '黑名单处置', general: '普通余额', game: '游戏余额', donation: '捐赠累计', paper: '画纸', brush: '画笔', sameIdentity: '查看同 Discord 的所有账号', alert: '关联告警', aborts: '主动删号中止的对局（近 90 天）', noAborts: '没有保留的中止记录', previous: '上一页', next: '下一页', match: '对局', occurred: '发生时间' },
} as const;

export const accountHistoryCopy = {
  en: {
    coverage: 'History contains only information retained when the account was deleted. Unrecorded details are shown as unknown.',
    sources: { self: 'Account holder', admin: 'Administrator', system: 'System', unknown: 'Unknown' },
    actions: { none: 'No automatic action', added: 'Added to blacklist', appended: 'Added a note to the existing blacklist entry', unknown: 'Unknown' },
    reasons: { deletion_penalty_evasion: 'Attempted to evade restrictions through account deletion', deletion_debt_evasion: 'Attempted to evade debt through account deletion' },
    games: { bidding: 'Bidding', likes: 'Likes' },
  },
  zh: {
    coverage: '历史记录仅包含删号时已保留的信息，未记录的项目显示为“未知”。',
    sources: { self: '用户主动删除', admin: '管理员删除', system: '系统删除', unknown: '未知' },
    actions: { none: '未自动处置', added: '已加入黑名单', appended: '已向现有黑名单追加说明', unknown: '未知' },
    reasons: { deletion_penalty_evasion: '试图通过删号逃避处罚', deletion_debt_evasion: '试图通过删号逃避负债' },
    games: { bidding: '竞标', likes: '点赞' },
  },
} as const;
