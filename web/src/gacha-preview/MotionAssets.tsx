import { useEffect, useState } from 'react';
import { ArtworkImage } from '@shared/artwork/ArtworkImage';
import { useArtworkPolicy } from '@shared/artwork/context';
import { Field } from '@shared/components/ui/Field';
import { cardById } from '../user/activities/gacha/catalog';

export type LayerResource = {
  name: string;
  label: string;
  path: string;
  rect?: readonly [number, number, number, number];
};
export type LayerScope = 'portrait' | 'light' | 'dark';
export type LayerLoader = (
  id: string,
  scope: LayerScope,
  high: boolean,
  signal: AbortSignal,
) => Promise<readonly LayerResource[]>;

function LayerImage({ layer, card }: { layer: LayerResource; card: string }) {
  const [size, setSize] = useState<[number, number]>();
  return (
    <figure className="seasonal-review__resource">
      <a
        className="seasonal-review__resource-image"
        href={layer.path}
        target="_blank"
        rel="noreferrer"
      >
        <ArtworkImage
          artworkKey={'card:' + card}
          src={layer.path}
          alt={layer.label}
          loading="lazy"
          onLoad={(event) =>
            setSize([event.currentTarget.naturalWidth, event.currentTarget.naturalHeight])
          }
        />
      </a>
      <figcaption>
        <strong>{layer.label}</strong>
        {size && (
          <span>
            {size[0]} × {size[1]}
          </span>
        )}
        {layer.rect && (
          <span>
            原画位置 {layer.rect[0]}, {layer.rect[1]}
          </span>
        )}
        <a href={layer.path} target="_blank" rel="noreferrer">
          打开独立图层 ↗
        </a>
      </figcaption>
    </figure>
  );
}

export function MotionAssets({
  ids,
  high,
  load,
}: {
  ids: readonly string[];
  high: boolean;
  load: LayerLoader;
}) {
  const [selected, setSelected] = useState(ids[0]);
  const [scope, setScope] = useState<LayerScope>('portrait');
  const [backdrop, setBackdrop] = useState('checker');
  const [result, setResult] = useState<{
    key: string;
    layers: readonly LayerResource[];
    error?: string;
  }>();
  const policy = useArtworkPolicy();
  const allowed = policy.allowed('card:' + selected);
  const key = `${selected}:${scope}:${high}`;
  useEffect(() => {
    if (!allowed) return;
    const controller = new AbortController();
    void load(selected, scope, high, controller.signal).then(
      (layers) => {
        if (!controller.signal.aborted) setResult({ key, layers });
      },
      () => {
        if (!controller.signal.aborted)
          setResult({ key, layers: [], error: '图层未加载，请刷新后重试。' });
      },
    );
    return () => controller.abort();
  }, [selected, scope, high, allowed, key, load]);
  const appearance = cardById[selected].rarity === 'SP';
  return (
    <section
      id="resources"
      className="seasonal-review__resources"
      aria-labelledby="resources-title"
    >
      <h2 id="resources-title">独立图层</h2>
      <p>按原画位置登记的衣料、头发、配饰和环境素材。切换底色可以检查透明边缘。</p>
      <div className="seasonal-review__resource-controls">
        <Field label="资源角色">
          {(props) => (
            <select
              {...props}
              className="nb-select"
              value={selected}
              onChange={(event) => {
                setSelected(event.target.value);
                setScope('portrait');
              }}
            >
              {ids.map((id) => (
                <option key={id} value={id}>
                  {cardById[id].name}
                </option>
              ))}
            </select>
          )}
        </Field>
        <Field label="透明层底色">
          {(props) => (
            <select
              {...props}
              className="nb-select"
              value={backdrop}
              onChange={(event) => setBackdrop(event.target.value)}
            >
              <option value="checker">棋盘格</option>
              <option value="light">浅色</option>
              <option value="dark">深色</option>
            </select>
          )}
        </Field>
        {appearance && (
          <Field label="素材用途">
            {(props) => (
              <select
                {...props}
                className="nb-select"
                value={scope}
                onChange={(event) => setScope(event.target.value as LayerScope)}
              >
                <option value="portrait">动态卡牌</option>
                <option value="light">浅色动态主题</option>
                <option value="dark">深色动态主题</option>
              </select>
            )}
          </Field>
        )}
      </div>
      {!allowed ? (
        <p>请先启用本页的素材显示开关。</p>
      ) : result?.key !== key ? (
        <p role="status">正在读取图层…</p>
      ) : result.error ? (
        <p role="alert">{result.error}</p>
      ) : (
        <div className="seasonal-review__resource-grid" data-backdrop={backdrop}>
          {result.layers.map((layer) => (
            <LayerImage key={layer.path} layer={layer} card={selected} />
          ))}
        </div>
      )}
    </section>
  );
}
