import { useState } from 'react';
import { useQueryClient } from '@tanstack/react-query';
import { Card, ErrorState } from '@shared/components/States';
import { parameterLabel, usePictureBookText } from '@shared/picturebook/copy';
import {
  initialValues,
  retainModelValues,
  prepareSubmission,
  previewPrice,
  type ParameterValues,
} from '@shared/picturebook/parameters';
import { ModelParameterFields } from '@shared/picturebook/ModelParameterFields';
import { quoteTask, submitTask } from '@shared/picturebook/publicApi';
import { type ImageModel, type ImageTask, type SubmitInput } from '@shared/picturebook/publicTypes';
import { useImageOperation } from '@shared/picturebook/useImageOperation';
import type { ActivityWallet } from '@shared/limitedactivities/api';
import { economySessionRequest } from '../economy/queries';
import { useImageReconcile, pictureBookKeys } from './queries';

function Fields({
  model,
  locked,
  permitted,
  wallet,
  uncertain,
  pending,
  submit,
  quote,
  onRefresh,
  initialDraft,
  onDraftChange,
  error,
}: {
  readonly model: ImageModel;
  readonly locked: boolean;
  readonly permitted: boolean;
  readonly wallet?: ActivityWallet;
  readonly uncertain: boolean;
  readonly pending: boolean;
  readonly submit: (input?: SubmitInput) => void;
  readonly quote: (input: SubmitInput) => ReturnType<typeof quoteTask>;
  readonly onRefresh: () => void;
  readonly initialDraft?: ParameterValues;
  readonly onDraftChange: (values: ParameterValues) => void;
  readonly error: unknown;
}) {
  const t = usePictureBookText();
  const [values, setValues] = useState<ParameterValues>(() => initialDraft ?? initialValues(model)),
    [attempted, setAttempted] = useState(false);
  const [quoting, setQuoting] = useState(false),
    [quoted, setQuoted] = useState<Awaited<ReturnType<typeof quoteTask>> | null>(null),
    [quoteError, setQuoteError] = useState<unknown>(null);
  const updateValues = (patch: ParameterValues) => {
    const next = { ...values, ...patch };
    setValues(next);
    onDraftChange(next);
    setQuoted(null);
    setQuoteError(null);
  };
  const prepared = prepareSubmission(model, values);
  const displayedPrice = previewPrice(model, values);
  const total = displayedPrice?.total;
  const enough =
    wallet &&
    total &&
    BigInt(wallet.sketch_paper) >= BigInt(total.paper) &&
    BigInt(wallet.sketch_brush) >= BigInt(total.brush);
  const problem = 'problem' in prepared ? prepared.problem : undefined;
  return (
    <form
      className="picturebook-form"
      onSubmit={(event) => {
        event.preventDefault();
        setAttempted(true);
        if (uncertain) submit();
        else if (!problem && enough && permitted && 'input' in prepared && !quoting) {
          setQuoting(true);
          setQuoteError(null);
          void quote(prepared.input)
            .then((current) => {
              if (
                current.model_revision !== model.revision ||
                current.pricing_revision !== model.pricing_revision ||
                current.total.paper !== prepared.price.paper ||
                current.total.brush !== prepared.price.brush ||
                current.unit.paper !== prepared.unit.paper ||
                current.unit.brush !== prepared.unit.brush
              ) {
                setQuoted(current);
                onRefresh();
                return;
              }
              submit(prepared.input);
            })
            .catch(setQuoteError)
            .finally(() => setQuoting(false));
        }
      }}
    >
      <fieldset disabled={locked}>
        <ModelParameterFields model={model} values={values} onChange={updateValues} />
      </fieldset>
      <dl className="picturebook-facts">
        <dt>{t('每张价格', 'Price per image')}</dt>
        <dd>
          {displayedPrice
            ? displayedPrice.unit.paper +
              ' ' +
              t('草稿纸', 'paper') +
              ' + ' +
              displayedPrice.unit.brush +
              ' ' +
              t('画笔', 'brushes')
            : t('此尺寸暂无可用价格', 'No price is available for this size')}
        </dd>
        {displayedPrice ? (
          <>
            <dt>{t('计价依据', 'Price basis')}</dt>
            <dd>
              {displayedPrice.basis === 'size'
                ? t('精确尺寸', 'Exact size')
                : displayedPrice.basis === 'tier'
                  ? t('尺寸档位', 'Size tier')
                  : displayedPrice.basis === 'auto'
                    ? t('自动尺寸', 'Automatic size')
                    : t('默认价格', 'Default price')}
            </dd>
          </>
        ) : null}
        <dt>{t('本次预扣', 'Reservation')}</dt>
        <dd>
          <output>
            {total
              ? total.paper +
                ' ' +
                t('草稿纸', 'paper') +
                ' + ' +
                total.brush +
                ' ' +
                t('画笔', 'brushes')
              : '—'}
          </output>
        </dd>
      </dl>
      {wallet && total && !enough ? (
        <p role="status">
          {t(
            '活动币余额不足，请先兑换。',
            'Your activity balance is insufficient. Exchange credits first.',
          )}
        </p>
      ) : null}
      {quoted ? (
        <p role="status">
          {t('模型或价格已变化。当前整单价格：', 'The model or price changed. Current total: ')}
          {quoted.total.paper} {t('草稿纸', 'paper')} + {quoted.total.brush} {t('画笔', 'brushes')}
          {t('。请载入最新配置后再确认。', '. Load the latest configuration before confirming.')}
        </p>
      ) : null}
      {attempted && problem ? (
        <p role="alert">
          {problem === 'combination'
            ? t(
                '这些参数不能组合使用，请调整后再提交。',
                'This parameter combination is not supported.',
              )
            : problem === 'prompt_size'
              ? t('提示词或请求超过长度限制。', 'The prompt or request exceeds the length limit.')
              : problem === 'price'
                ? t(
                    '当前尺寸无可用价格，或总价超出限制。',
                    'This size has no price, or the total exceeds its limit.',
                  )
                : t('请检查参数：', 'Check parameter: ') + parameterLabel(problem, t)}
        </p>
      ) : null}
      {uncertain ? (
        <p role="status">
          {t(
            '提交结果尚未确认。请重试同一次提交；内容和价格保持原样，不会重复扣币。',
            'The submission result is unconfirmed. Retry the same submission with its original content and price; it will not charge twice.',
          )}
        </p>
      ) : null}
      {quoteError ? <ErrorState error={quoteError} /> : null}
      {error ? <ErrorState error={error} /> : null}
      <button
        className="btn btn-primary"
        type="submit"
        disabled={pending || quoting || (!uncertain && (!permitted || !wallet || !enough))}
      >
        {uncertain
          ? t('重试同一次提交', 'Retry the same submission')
          : pending || quoting
            ? t('正在提交', 'Submitting')
            : t('确认预扣并加入队列', 'Reserve currency and join queue')}
      </button>
    </form>
  );
}
export function ModelForm({
  account,
  models,
  available,
  wallet,
  onAccepted,
}: {
  readonly account: string;
  readonly models: ImageModel[];
  readonly available: boolean;
  readonly wallet?: ActivityWallet;
  readonly onAccepted: (task: ImageTask) => void;
}) {
  const t = usePictureBookText(),
    reconcile = useImageReconcile(account),
    client = useQueryClient();
  const [snapshot, setSnapshot] = useState<ImageModel | undefined>(models[0]),
    [formGeneration, setFormGeneration] = useState(0),
    [drafts, setDrafts] = useState<Record<string, ParameterValues>>({});
  const operation = useImageOperation('steward', submitTask, reconcile);
  const latest = models.find((model) => model.id === snapshot?.id);
  const stale =
    !latest ||
    latest.revision !== snapshot?.revision ||
    latest.pricing_revision !== snapshot?.pricing_revision;
  return (
    <Card>
      <h2>{t('创作图片', 'Create images')}</h2>
      <div className="picturebook-form">
        <label>
          {t('图像模型', 'Image model')}
          <select
            disabled={operation.locked}
            value={snapshot?.id ?? ''}
            onChange={(event) => {
              const next = models.find((model) => model.id === event.target.value);
              if (next && snapshot && !drafts[next.id]) {
                const previous = drafts[snapshot.id] ?? initialValues(snapshot);
                setDrafts((old) => ({ ...old, [next.id]: retainModelValues(next, previous) }));
              }
              setSnapshot(next);
            }}
          >
            {!snapshot || !latest ? (
              <option value={snapshot?.id ?? ''}>
                {t('请选择可用模型', 'Select an available model')}
              </option>
            ) : null}
            {models.map((model) => (
              <option key={model.id} value={model.id}>
                {model.display_name}
              </option>
            ))}
          </select>
        </label>
      </div>
      {snapshot ? (
        <>
          <p className="picturebook-prewrap">{snapshot.description}</p>
          {stale ? (
            <div role="status">
              <p>
                {t(
                  '模型配置已变化，请载入最新配置后再提交。',
                  'The model configuration changed. Load the latest configuration before submitting.',
                )}
              </p>
              {latest ? (
                <button
                  className="btn btn-secondary"
                  disabled={operation.locked}
                  onClick={() => setSnapshot(latest)}
                >
                  {t('载入最新配置', 'Load latest configuration')}
                </button>
              ) : null}
            </div>
          ) : null}
          {!available ? (
            <p role="status">
              {t(
                '当前不能提交新任务。已受理任务仍可在下方查看。',
                'New submissions are unavailable. You can still view accepted tasks below.',
              )}
            </p>
          ) : null}
          <Fields
            key={
              snapshot.id +
              ':' +
              snapshot.revision +
              ':' +
              snapshot.pricing_revision +
              ':' +
              formGeneration
            }
            model={snapshot}
            locked={operation.locked}
            permitted={available && !stale}
            wallet={wallet}
            uncertain={operation.uncertain}
            pending={operation.pending}
            error={operation.error}
            initialDraft={drafts[snapshot.id]}
            onDraftChange={(values) => setDrafts((old) => ({ ...old, [snapshot.id]: values }))}
            quote={(input) => economySessionRequest(client, () => quoteTask(input), account)}
            onRefresh={() =>
              void client.invalidateQueries({ queryKey: pictureBookKeys.models(account) })
            }
            submit={(input) => {
              const request = operation.input ?? input;
              if (request)
                void operation.run(request, (task) => {
                  onAccepted(task);
                  setDrafts((old) => ({ ...old, [snapshot.id]: initialValues(snapshot) }));
                  setFormGeneration((value) => value + 1);
                });
            }}
          />
        </>
      ) : (
        <p>{t('暂无可用图像模型。', 'No image models are available.')}</p>
      )}
    </Card>
  );
}
