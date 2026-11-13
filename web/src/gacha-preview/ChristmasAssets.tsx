import { useState } from 'react';
import { ArtworkImage } from '@shared/artwork/ArtworkImage';
import { Field } from '@shared/components/ui/Field';
import { cardById, cards } from '../user/activities/gacha/catalog';

const christmasCards = cards.filter((card) => card.theme === 't');

function Resource({
  title,
  src,
  artworkKey,
  width,
  height,
}: {
  title: string;
  src: string;
  artworkKey: string;
  width: number;
  height: number;
}) {
  return (
    <figure className="seasonal-review__resource">
      <a
        className="seasonal-review__resource-image"
        href={src}
        target="_blank"
        rel="noreferrer"
        aria-label={title + ' · 打开原尺寸图片'}
      >
        <ArtworkImage
          artworkKey={artworkKey}
          src={src}
          alt={title}
          width={width}
          height={height}
          loading="lazy"
        />
      </a>
      <figcaption>
        <strong>{title}</strong>
        <span>
          {width} × {height}
        </span>
        <a href={src} target="_blank" rel="noreferrer">
          打开原尺寸图片 ↗
        </a>
      </figcaption>
    </figure>
  );
}

export function ChristmasAssets({ high }: { high: boolean }) {
  const [selected, setSelected] = useState('t-chatgpt');
  const [backdrop, setBackdrop] = useState('checker');
  const card = cardById[selected];
  const root = `/assets/gacha/${high ? 'hires/' : ''}`;
  const portrait = high ? [2560, 3840] : [1024, 1536];
  const landscape = high ? [3840, 2160] : [1672, 941];
  return (
    <section
      id="resources"
      className="seasonal-review__resources"
      aria-labelledby="resources-title"
    >
      <h2 id="resources-title">分层资源与原图</h2>
      <p>选择角色查看各层素材；顶部「高清资源」同时切换这里的图片。点击图片可打开原尺寸。</p>
      <div className="seasonal-review__resource-controls">
        <Field label="资源角色">
          {(props) => (
            <select
              {...props}
              className="nb-select"
              value={selected}
              onChange={(event) => setSelected(event.target.value)}
            >
              {christmasCards.map((item) => (
                <option key={item.id} value={item.id}>
                  {item.character} · {item.name}
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
      </div>
      <div className="seasonal-review__resource-grid" data-backdrop={backdrop}>
        {[
          ['完整卡面', `cards/${selected}.webp`],
          ['人物透明层 · 睁眼', `motion/${selected}/hero.webp`],
          ['人物透明层 · 闭眼', `motion/${selected}/closed.webp`],
          ['卡面背景', `motion/${selected}/background.webp`],
        ].map(([label, path]) => (
          <Resource
            key={path}
            title={card.character + ' · ' + label}
            artworkKey={'card:' + selected}
            src={root + path}
            width={portrait[0]}
            height={portrait[1]}
          />
        ))}
        <Resource
          title={card.character + ' · 图鉴缩略图'}
          artworkKey={'card:' + selected}
          src={card.thumb}
          width={320}
          height={480}
        />
      </div>
      <h3>合图与封面的分层</h3>
      <div
        className="seasonal-review__resource-grid seasonal-review__resource-grid--wide"
        data-backdrop={backdrop}
      >
        {[
          ['五人合图 · 透明前景', 'layers/groups/t-foreground.webp', 'illustration:t'],
          ['五人合图 · 背景', 'backgrounds/t.webp', 'illustration:t'],
          ['卡池封面 · 透明前景', 'layers/covers/t-foreground.webp', 'cover:t'],
          ['卡池封面 · 背景', 'backgrounds/covers/t.webp', 'cover:t'],
        ].map(([title, path, key]) => (
          <Resource
            key={path}
            title={title}
            artworkKey={key}
            src={root + path}
            width={landscape[0]}
            height={landscape[1]}
          />
        ))}
      </div>
    </section>
  );
}
