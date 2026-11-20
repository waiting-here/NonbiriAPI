import { Suspense, use, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { CopyValue } from '@shared/components/CopyValue';
import { Button, Fold, Sheet, Tabs } from '@shared/components/ui';
import { MODEL_TYPE_LABEL_KEYS, MODEL_TYPE_PATHS } from '@shared/modelTypes';
import { CoreErrorPanel, CoreLoading } from './components';
import type { CatalogView, ClientModel, ModelBrowserProps, Source } from './ClientModelBrowser';

let browserChunk: Promise<typeof import('./ClientModelBrowser') | undefined> | undefined;
function loadModelBrowser() {
  browserChunk ??= import('./ClientModelBrowser').catch(() => {
    browserChunk = undefined;
    return undefined;
  });
  return browserChunk;
}

function ModelBrowserContent({
  module,
  retry,
  ...props
}: ModelBrowserProps & { module: ReturnType<typeof loadModelBrowser>; retry: () => void }) {
  const loaded = use(module);
  if (!loaded) return <CoreErrorPanel compact error={null} onRetry={retry} />;
  const ModelBrowser = loaded.default;
  return <ModelBrowser {...props} />;
}

const initialView: CatalogView = { page: '1', pageSize: 10, query: '', draft: '' };

function ModelUsage({ model }: { model: ClientModel }) {
  const { t } = useTranslation();
  const type = model.types.find(
    (value) => value === 'chat_completions' || value === 'responses' || value === 'embeddings',
  );
  const input = t('user.core.keys.sampleInput');
  const body =
    type === 'chat_completions'
      ? { model: model.name, messages: [{ role: 'user', content: input }] }
      : { model: model.name, input };
  return (
    <Fold plain lazy title={t('user.core.keys.supportedPaths')}>
      <ul className="api-model-paths">
        {model.types.map((supported) => (
          <li key={supported}>
            <span>{t(MODEL_TYPE_LABEL_KEYS[supported])}</span>
            <code>{MODEL_TYPE_PATHS[supported]}</code>
          </li>
        ))}
      </ul>
      {type ? (
        <div className="api-model-example">
          <h4>{t('user.core.keys.exampleBody')}</h4>
          <p className="nb-small nb-muted">
            {t('user.core.keys.exampleBilling', { path: MODEL_TYPE_PATHS[type] })}
          </p>
          <CopyValue
            value={JSON.stringify(body, null, 2)}
            label={t('user.core.keys.exampleBody')}
            variant="secondary"
          />
        </div>
      ) : null}
    </Fold>
  );
}

export function ClientModelField({ accountId }: { accountId: string }) {
  const { t } = useTranslation();
  const [browserModule, setBrowserModule] = useState<ReturnType<typeof loadModelBrowser>>();
  const openBrowser = () => setBrowserModule(loadModelBrowser());
  const [source, setSource] = useState<Source>('charity');
  const [views, setViews] = useState({ charity: initialView, own: initialView });
  const [selected, setSelected] = useState<ClientModel>();
  return (
    <div className="api-client-row">
      <h3>{t('user.core.keys.modelName')}</h3>
      <div className="api-client-row__value">
        {selected ? (
          <div className="api-client-model">
            <CopyValue value={selected.name} label={selected.name} variant="secondary" />
          </div>
        ) : (
          <p className="nb-small nb-muted">{t('user.core.keys.modelHint')}</p>
        )}
        <div className="nb-actions">
          <Button type="button" aria-haspopup="dialog" onClick={openBrowser}>
            {t(selected ? 'user.core.keys.changeModel' : 'user.core.keys.showModelNames')}
          </Button>
        </div>
        {selected ? <ModelUsage model={selected} /> : null}
      </div>
      <Sheet
        open={Boolean(browserModule)}
        title={t('user.core.keys.showModelNames')}
        description={t('user.core.keys.pickerHelp')}
        closeLabel={t('common.close')}
        onClose={() => setBrowserModule(undefined)}
      >
        <Tabs<Source>
          value={source}
          label={t('user.core.keys.modelSource')}
          onChange={setSource}
          tabs={[
            { value: 'charity', label: t('user.core.keys.charityModels') },
            { value: 'own', label: t('user.core.keys.ownModels') },
          ]}
        >
          <Suspense fallback={<CoreLoading compact />}>
            {browserModule ? (
              <ModelBrowserContent
                module={browserModule}
                retry={openBrowser}
                key={source}
                accountId={accountId}
                source={source}
                view={views[source]}
                selected={selected}
                updateView={(changes) =>
                  setViews((current) => ({
                    ...current,
                    [source]: { ...current[source], ...changes },
                  }))
                }
                onChoose={(model) => {
                  setSelected(model);
                  setBrowserModule(undefined);
                }}
              />
            ) : null}
          </Suspense>
        </Tabs>
      </Sheet>
    </div>
  );
}
