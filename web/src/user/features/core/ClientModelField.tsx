import { Suspense, use, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { CopyValue } from '@shared/components/CopyValue';
import { Button, Fold, Sheet, Tabs } from '@shared/components/ui';
import { MODEL_TYPE_LABEL_KEYS, MODEL_TYPE_PATHS, type ModelType } from '@shared/modelTypes';
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

function shellQuote(value: string) {
  return "'" + value.replaceAll("'", "'\\''") + "'";
}

function sampleRequest(type: ModelType, name: string, input: string) {
  const url = shellQuote(window.location.origin + MODEL_TYPE_PATHS[type]);
  const curl = `curl --fail-with-body ${url} -H "Authorization: Bearer $NONBIRI_KEY"`;
  if (
    type === 'audio_transcriptions' ||
    type === 'audio_translations' ||
    type === 'images_edits' ||
    type === 'images_variations'
  ) {
    const file = type.startsWith('audio_') ? 'file=@audio.mp3' : 'image=@image.png';
    return {
      curl: `${curl} --form-string ${shellQuote('model=' + name)} -F ${shellQuote(file)}${type === 'images_edits' ? ' --form-string ' + shellQuote('prompt=' + input) : ''}`,
    };
  }
  const body: Record<string, unknown> = { model: name };
  switch (type) {
    case 'chat_completions':
      body.messages = [{ role: 'user', content: input }];
      break;
    case 'images_generations':
    case 'completions':
      body.prompt = input;
      break;
    case 'responses_compact':
      body.input = [{ role: 'user', content: input }];
      break;
    case 'decisions':
      body.input = input;
      body.questions = [{ type: 'predicate', instructions: input }];
      break;
    case 'systemone':
      body.state = 'question';
      body.questions = { q: { type: 'noul' } };
      break;
    case 'rerank':
      body.query = input;
      body.documents = [input];
      break;
    case 'audio_speech':
      body.input = input;
      body.voice = 'alloy';
      break;
    default:
      body.input = input;
  }
  return {
    body,
    curl: `${curl} -H 'Content-Type: application/json' --data-binary ${shellQuote(JSON.stringify(body))}${type === 'audio_speech' ? ' --output speech.mp3' : ''}`,
  };
}

function ModelUsage({ model }: { model: ClientModel }) {
  const { t } = useTranslation();
  const [selected, setSelected] = useState<ModelType>();
  const type = selected && model.types.includes(selected) ? selected : model.types[0];
  const example = type ? sampleRequest(type, model.name, t('user.core.keys.sampleInput')) : null;
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
      {type && example ? (
        <div className="api-model-example">
          {model.types.length > 1 ? (
            <select
              aria-label={t('user.core.keys.supportedPaths')}
              value={type}
              onChange={(event) => setSelected(event.target.value as ModelType)}
            >
              {model.types.map((item) => (
                <option key={item} value={item}>
                  {t(MODEL_TYPE_LABEL_KEYS[item])}
                </option>
              ))}
            </select>
          ) : null}
          {example.body ? <h4>{t('user.core.keys.exampleBody')}</h4> : null}
          <p className="nb-small nb-muted">
            {t('user.core.keys.exampleBilling', { path: MODEL_TYPE_PATHS[type] })}
          </p>
          {example.body ? (
            <CopyValue
              value={JSON.stringify(example.body, null, 2)}
              label={t('user.core.keys.exampleBody')}
              variant="secondary"
            />
          ) : null}
          <CopyValue value={example.curl} label="cURL" variant="secondary" />
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
