import { useTranslation } from 'react-i18next';

export type HelpTopic =
  'service' | 'serviceKey' | 'callerKey' | 'upstreamModel' | 'callName' | 'storeFalse' | 'harness';
const topics: Record<HelpTopic, string> = {
  service: 'common.help.service',
  serviceKey: 'common.help.serviceKey',
  callerKey: 'common.help.callerKey',
  upstreamModel: 'common.help.upstreamModel',
  callName: 'common.help.callName',
  storeFalse: 'common.help.storeFalse',
  harness: 'common.help.harness',
};

export function FieldHelp({ topic, id }: { topic: HelpTopic; id?: string }) {
  const { t } = useTranslation();
  return (
    <p id={id} className="nb-field-help">
      {t(topics[topic])}
    </p>
  );
}
