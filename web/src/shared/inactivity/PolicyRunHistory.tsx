import { DataTable } from '@shared/components/ui';
import { useDateTimeFormatter } from '@shared/utils/datetime';
import { credits, getRuns } from './api';
import { PolicyHistory } from './PolicyHistory';

export function PolicyRunHistory({ zh, locale }: { zh: boolean; locale?: string }) {
  const formatDateTime = useDateTimeFormatter();
  const title = zh ? '执行记录' : 'Execution records';
  return (
    <PolicyHistory kind="runs" title={title} zh={zh} load={getRuns}>
      {(rows) => (
        <DataTable
          caption={title}
          rows={rows}
          rowKey={(item) => item.id}
          columns={[
            {
              key: 'account',
              header: zh ? '账号' : 'Account',
              cell: 'title',
              render: (item) => item.user_id ?? (zh ? '已去标识' : 'Deidentified'),
            },
            {
              key: 'action',
              header: zh ? '动作' : 'Action',
              cell: 'status',
              render: (item) =>
                item.action === 'decay'
                  ? zh
                    ? '积分衰减'
                    : 'Decay'
                  : zh
                    ? '保护封禁'
                    : 'Protective ban',
            },
            {
              key: 'date',
              header: zh ? '时间' : 'Time',
              cell: 'meta',
              mobileLabel: zh ? '时间' : 'Time',
              render: (item) => formatDateTime(item.created_at, locale === 'zh' ? 'zh' : 'en'),
            },
            {
              key: 'credits',
              header: zh ? '通用／游戏积分' : 'General / game credits',
              cell: 'meta',
              mobileLabel: zh ? '通用／游戏积分' : 'General / game credits',
              render: (item) => `${credits(item.general_milli)} / ${credits(item.game_milli)}`,
            },
          ]}
        />
      )}
    </PolicyHistory>
  );
}
