import { useTranslation } from 'react-i18next';
import type { BindingGuide, InterfaceCoverage } from '@shared/operations/charity';
import type { ServiceRoute } from '@shared/connections';
import { MODEL_TYPE_LABEL_KEYS, MODEL_TYPE_PATHS } from '@shared/modelTypes';
import { NATIVE_SERVICE_LABEL_KEYS, type NativeService } from '@shared/nativeServices';
import { NativeServicesField } from '../NativeServicesField';
import { serviceStateLabel } from '@shared/operations/resourceHealth';
import { Fold, StatusBadge } from '../ui';
import './binding-routes.css';

function conversionHelp(route: Pick<ServiceRoute, 'operation' | 'native_service'>) {
  switch (route.native_service) {
    case 'responses':
      return 'common.bindingGuide.chatToResponses';
    case 'chat_completions':
      return 'common.bindingGuide.responsesToChat';
    case 'anthropic_messages':
      return 'common.bindingGuide.anthropic';
    case 'gemini_generate_content':
      return 'common.bindingGuide.gemini';
    case 'gateway_text':
      return route.operation === 'responses'
        ? 'common.bindingGuide.responsesToGateway'
        : 'common.bindingGuide.chatToGateway';
    default:
      return 'common.bindingGuide.limited';
  }
}

export function BindingRouteGuide({
  guide,
  selected,
}: {
  guide: BindingGuide;
  selected: readonly NativeService[];
}) {
  const { t } = useTranslation();
  return (
    <div className="binding-routes">
      {guide.interfaces.map((operation) => {
        const routes = guide.routes.filter(
          (route) => route.operation === operation && selected.includes(route.native_service),
        );
        return (
          <div className="binding-routes__interface" key={operation}>
            <code className="mono">{MODEL_TYPE_PATHS[operation]}</code>
            {routes.length ? (
              <ul>
                {routes.map((route) => {
                  const service = guide.services.find(
                    (item) => item.native_service === route.native_service,
                  );
                  return (
                    <li key={route.native_service}>
                      <span>
                        {t(
                          route.mode === 'native'
                            ? 'common.bindingGuide.direct'
                            : 'common.bindingGuide.converted',
                          { service: t(NATIVE_SERVICE_LABEL_KEYS[route.native_service]) },
                        )}
                      </span>
                      {service?.state && service.state !== 'available' ? (
                        <strong className="binding-routes__attention">
                          {t(serviceStateLabel(service.state))}
                        </strong>
                      ) : null}
                      {route.mode === 'converted' ? <p>{t(conversionHelp(route))}</p> : null}
                    </li>
                  );
                })}
              </ul>
            ) : (
              <p>{t('common.bindingGuide.uncovered')}</p>
            )}
          </div>
        );
      })}
    </div>
  );
}

export function BindingServiceEditor({
  guide,
  options,
  value,
  onChange,
  disabled,
}: {
  guide?: BindingGuide;
  options: NativeService[];
  value: NativeService[];
  onChange: (services: NativeService[]) => void;
  disabled?: boolean;
}) {
  const { t } = useTranslation();
  const required =
    options.length > 0 && value.length === 0 ? t('common.bindingGuide.required') : undefined;
  if (!guide)
    return (
      <NativeServicesField
        options={options}
        value={value}
        onChange={onChange}
        disabled={disabled}
        error={required}
      />
    );
  const affected = guide.services.filter(
    (item) => value.includes(item.native_service) && item.state && item.state !== 'available',
  );
  const selectedRoutes = guide.routes.filter((route) => value.includes(route.native_service));
  const needsAnotherSource =
    selectedRoutes.length > 0 &&
    selectedRoutes.every((route) =>
      affected.some((item) => item.native_service === route.native_service),
    );
  return (
    <Fold
      plain
      lazy
      title={t('common.bindingGuide.editServices')}
      meta={
        affected.length
          ? t('common.bindingGuide.checkHealth', { count: affected.length })
          : undefined
      }
      summary={
        value.length
          ? value.map((service) => t(NATIVE_SERVICE_LABEL_KEYS[service])).join(' · ')
          : t('common.bindingGuide.chooseService')
      }
    >
      <NativeServicesField
        options={options}
        value={value}
        onChange={onChange}
        disabled={disabled}
        error={required}
        help={t(
          needsAnotherSource ? 'common.bindingGuide.pausedHelp' : 'common.bindingGuide.chooseHelp',
        )}
        describeService={(service) => {
          const state = guide.services.find((item) => item.native_service === service)?.state;
          const routes = guide.routes.filter((route) => route.native_service === service);
          return (
            <span className="binding-service-description">
              {state && state !== 'available' && (
                <StatusBadge tone="warn">{t(serviceStateLabel(state))}</StatusBadge>
              )}
              {routes.length ? (
                (['native', 'converted'] as const).map((mode) => {
                  const names = routes
                    .filter((route) => route.mode === mode)
                    .map((route) => t(MODEL_TYPE_LABEL_KEYS[route.operation]));
                  return (
                    names.length > 0 && (
                      <span key={mode}>
                        {t(
                          mode === 'native'
                            ? 'common.bindingGuide.servesDirect'
                            : 'common.bindingGuide.servesConverted',
                          { interfaces: names.join(' · ') },
                        )}
                      </span>
                    )
                  );
                })
              ) : (
                <span>{t('common.bindingGuide.noInterface')}</span>
              )}
            </span>
          );
        }}
      />
      <BindingRouteGuide guide={guide} selected={value} />
    </Fold>
  );
}

export function ModelBindingCoverage({ coverage }: { coverage: InterfaceCoverage[] }) {
  const { t } = useTranslation();
  return (
    <section className="binding-coverage">
      <h4>{t('common.bindingGuide.coverage')}</h4>
      <ul>
        {coverage.map(
          ({
            operation: op,
            native_targets: native,
            converted_targets: converted,
            conversion_services: services,
          }) => {
            return (
              <li key={op}>
                <strong>{t(MODEL_TYPE_LABEL_KEYS[op])}</strong>
                <span>{t('common.bindingGuide.counts', { native, converted })}</span>
                {!native && !converted ? <p>{t('common.bindingGuide.noneReady')}</p> : null}
                {!native && converted > 0 ? (
                  <>
                    <p>{t('common.bindingGuide.convertedOnly')}</p>
                    <ul>
                      {services.map((service) => (
                        <li key={service}>
                          {t(NATIVE_SERVICE_LABEL_KEYS[service])} ·{' '}
                          {t(conversionHelp({ operation: op, native_service: service }))}
                        </li>
                      ))}
                    </ul>
                  </>
                ) : null}
              </li>
            );
          },
        )}
      </ul>
    </section>
  );
}
