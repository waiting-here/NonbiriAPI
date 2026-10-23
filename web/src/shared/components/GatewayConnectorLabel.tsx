import { useTranslation } from 'react-i18next';

export function GatewayConnectorLabel({ value }: { value: string | null | undefined }) {
  const { t } = useTranslation();
  return (
    <>
      {value === 'ai-sdk-gateway-v4'
        ? t('common.gatewayV4Name')
        : value === 'ai-sdk-gateway-v3'
          ? t('common.gatewayName')
          : value}
    </>
  );
}
