import { useCallback, useEffect, useRef, useState } from 'react';
import { Button } from '@shared/components/ui/Button';
import { Field } from '@shared/components/ui/Field';
import { Segmented } from '@shared/components/ui/Segmented';
import { highResolutionArt } from '../user/activities/gacha/art-resolution';
import { tomorrowAssets } from '../user/activities/gacha/tomorrow/assets';
import { previewControlMessage, previewReadyMessage } from './controls';
import {
  appearanceArt,
  dialogue,
  entries,
  entryName,
  introduction,
  type Entry,
  type View,
} from './fixture';
import { SceneArtwork } from './SceneArtwork';

export type Inspection = { entry: Entry; variant: 'card' | 'cover' | 'illustration' };

export function ReviewPanel({
  entry,
  setEntry,
  side,
  view,
  animated,
  dark,
  english,
  paused,
  inspect,
}: {
  entry: Entry;
  setEntry: (id: string) => void;
  side: string;
  view: View;
  animated: boolean;
  dark: boolean;
  english: boolean;
  paused: boolean;
  inspect: (next: Inspection) => void;
}) {
  const iframe = useRef<HTMLIFrameElement>(null);
  const [line, setLine] = useState('0');
  const t = (zh: string, en: string) => (english ? en : zh);
  const name = entryName(entry, english);
  const scene =
    '?scene=' +
    encodeURIComponent(entry.id) +
    '&theme=' +
    (dark ? 'dark' : 'light') +
    '&motion=' +
    (animated ? '1' : '0') +
    '&lang=' +
    (english ? 'en' : 'zh');
  const sendControl = useCallback(() => {
    iframe.current?.contentWindow?.postMessage(
      { type: previewControlMessage, paused },
      location.origin,
    );
  }, [paused]);
  useEffect(() => sendControl(), [sendControl]);
  useEffect(() => {
    const receive = (event: MessageEvent) => {
      if (event.origin !== location.origin || event.source !== iframe.current?.contentWindow)
        return;
      if (event.data?.type === previewReadyMessage) sendControl();
    };
    window.addEventListener('message', receive);
    return () => window.removeEventListener('message', receive);
  }, [sendControl]);
  const src = view === 'cover' ? entry.cover : entry.illustration;
  const files = [
    [t('完整卡面', 'Card'), entry.art],
    [t('奖池封面', 'Pool cover'), entry.cover],
    [t('单人插画', 'Illustration'), entry.illustration],
    [
      entry.kind === 'tomorrow'
        ? t('浅色装扮', 'Light appearance')
        : t('浅色背景', 'Light scenery'),
      appearanceArt(entry, false),
    ],
    [
      entry.kind === 'tomorrow' ? t('深色装扮', 'Dark appearance') : t('深色背景', 'Dark scenery'),
      appearanceArt(entry, true),
    ],
  ];
  return (
    <section className="sp-review__entry" aria-label={name}>
      <div className="sp-review__entry-head">
        <Field label={side}>
          {(props) => (
            <select {...props} value={entry.id} onChange={(event) => setEntry(event.target.value)}>
              {entries.map((item) => (
                <option key={item.id} value={item.id}>
                  {entryName(item, english)}
                  {item.kind === 'tomorrow' ? t(' · 新候选', ' · New candidate') : ''}
                </option>
              ))}
            </select>
          )}
        </Field>
        <p>
          <span className={'sp-review__badge' + (entry.kind === 'tomorrow' ? ' is-candidate' : '')}>
            {entry.kind === 'tomorrow' ? t('新候选', 'New candidate') : t('已发布', 'Published')}
          </span>
          <span>{english ? entry.noteEN : entry.note}</span>
        </p>
      </div>
      {view === 'card' ? (
        <>
          <button
            className="sp-review__card"
            onClick={() => inspect({ entry, variant: 'card' })}
            aria-label={t('放大查看', 'Inspect') + ' ' + name}
          >
            <SceneArtwork
              key={entry.id}
              entry={entry}
              variant="card"
              dark={dark}
              animated={animated}
              paused={paused}
              english={english}
              className="sp-review__painting"
            />
          </button>
          <div className="sp-review__actions sp-review__entry-actions">
            <Button variant="primary" onClick={() => inspect({ entry, variant: 'card' })}>
              {t('放大查看', 'Inspect card')}
            </Button>
          </div>
          {entry.kind === 'tomorrow' && (
            <div className="sp-review__dialogue">
              <p>{introduction[english ? 1 : 0]}</p>
              <Segmented
                label={t('台词', 'Dialogue')}
                value={line}
                options={dialogue.map((item, index) => ({
                  value: String(index),
                  label: item.label[english ? 1 : 0],
                }))}
                onChange={setLine}
              />
              <blockquote>{dialogue[Number(line)].line[english ? 1 : 0]}</blockquote>
            </div>
          )}
        </>
      ) : view === 'appearance' ? (
        <>
          <iframe
            ref={iframe}
            className="sp-review__scene-frame"
            src={scene}
            title={name + t('装扮预览', ' appearance preview')}
            onLoad={sendControl}
          />
          <div className="sp-review__entry-actions">
            <a className="nb-btn nb-btn--secondary" href={scene} target="_blank" rel="noreferrer">
              {t('独立窗口查看', 'Open full view')}
            </a>
          </div>
        </>
      ) : view === 'materials' ? (
        <div className="sp-review__materials">
          {files.map(([label, url]) => (
            <div className="sp-review__file" key={label}>
              <img src={url} alt={label} loading="lazy" />
              <div>
                <strong>{label}</strong>
                <a href={url} target="_blank" rel="noreferrer">
                  {t('标准图', 'Standard')}
                </a>
                <a href={highResolutionArt(url)} target="_blank" rel="noreferrer">
                  {t('高清图', 'High resolution')}
                </a>
              </div>
            </div>
          ))}
          {entry.kind === 'published' ? (
            <div className="sp-review__file">
              <img src={entry.thumb} alt={t('缩略图', 'Thumbnail')} loading="lazy" />
              <div>
                <strong>{t('缩略图', 'Thumbnail')}</strong>
                <a href={entry.thumb} target="_blank" rel="noreferrer">
                  256 × 384
                </a>
              </div>
            </div>
          ) : (
            <div className="sp-review__source-links">
              <a href={entry.thumb} target="_blank" rel="noreferrer">
                {t('缩略图（320 × 480）', 'Thumbnail (320 × 480)')}
              </a>
              <strong>{t('源图层', 'Source layers')}</strong>
              {[
                [t('人物', 'Character'), tomorrowAssets.hero],
                [t('闭眼校准图', 'Closed-eye registration'), tomorrowAssets.closed],
                [t('晨光背景', 'Morning scenery'), tomorrowAssets.backgroundLight],
                [t('蓝调背景', 'Evening scenery'), tomorrowAssets.backgroundDark],
                [t('云朵伙伴', 'Cloud companion'), tomorrowAssets.cloud],
              ].map(([label, url]) => (
                <a key={label} href={url} target="_blank" rel="noreferrer">
                  {label}
                </a>
              ))}
              <p className="sp-review__note">
                {t(
                  '原生人物为 1,024 × 1,536，背景为 1,672 × 941；大尺寸图由同一组分层重采样。',
                  'Native portrait: 1,024 × 1,536. Scenery: 1,672 × 941. Large exports are resampled from these same layers.',
                )}
              </p>
            </div>
          )}
        </div>
      ) : (
        <figure className="sp-review__wide">
          <button
            className="sp-review__wide-art"
            onClick={() => inspect({ entry, variant: view })}
            aria-label={t('放大查看', 'Inspect') + ' ' + name}
          >
            <SceneArtwork
              key={entry.id + view}
              entry={entry}
              variant={view}
              dark={dark}
              animated={animated}
              paused={paused}
              english={english}
              className="sp-review__painting"
            />
          </button>
          <figcaption>
            <Button size="sm" onClick={() => inspect({ entry, variant: view })}>
              {t('放大', 'Inspect')}
            </Button>
            <a href={src} target="_blank" rel="noreferrer">
              {t('标准图', 'Standard')}
            </a>
            <a href={highResolutionArt(src)} target="_blank" rel="noreferrer">
              {t('高清图', 'High resolution')}
            </a>
          </figcaption>
        </figure>
      )}
    </section>
  );
}
