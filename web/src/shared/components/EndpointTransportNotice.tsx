import { useTranslation } from 'react-i18next';
import { Note } from './ui/Note';

export function EndpointTransportNotice({ url }: { url: string }) {
  const { i18n } = useTranslation();
  if (!url.trim().toLowerCase().startsWith('http://')) return null;
  return (
    <Note tone="warn">
      {i18n.resolvedLanguage?.startsWith('zh')
        ? 'HTTP 连接未加密，密钥和请求内容可能被传输途中的第三方读取或修改。建议使用 HTTPS。'
        : 'HTTP is unencrypted. Third parties along the connection may read or alter API keys and request content. HTTPS is recommended.'}
    </Note>
  );
}
