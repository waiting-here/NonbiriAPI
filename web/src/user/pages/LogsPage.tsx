import { useTranslation } from 'react-i18next';
import { PageHeader } from '@shared/components/States';
import { RoleLogPanel } from '@shared/components/log';
import '@shared/operations/operations.css';

export function LogsPage() {
  const { i18n } = useTranslation();
  const zh = i18n.resolvedLanguage?.toLowerCase().startsWith('zh') ?? false;
  return (
    <div className="page ops-stack">
      <PageHeader
        eyebrow="Operations"
        title={zh ? '我的请求日志' : 'My request logs'}
        description={zh ? '查看逻辑请求结果、四类 Token 用量和仅属于你的安全上游尝试。' : 'Inspect logical request results, four token buckets, and owner-safe upstream attempts.'}
      />
      <RoleLogPanel role="user" language={i18n.resolvedLanguage} />
    </div>
  );
}
