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
        title={zh ? '请求日志' : 'Request logs'}
        description={zh ? '管理投影不包含用户逻辑模型名、私人备注、凭据或原始上游正文。' : 'The administrator projection excludes logical model names, private notes, credentials, and raw upstream bodies.'}
      />
      <RoleLogPanel role="admin" language={i18n.resolvedLanguage} />
    </div>
  );
}
