import { useCallback, useEffect, useRef, useState } from 'react';
import { Button } from '@shared/components/ui/Button';
import { Field } from '@shared/components/ui/Field';
import { Segmented } from '@shared/components/ui/Segmented';
import { highResolutionArt } from '../user/activities/gacha/art-resolution';
import {
  tomorrowAssets,
  refinedTomorrowAssets,
  turnBackTomorrowAssets,
  walkingTomorrowAssets,
} from '../user/activities/gacha/tomorrow/assets';
import { previewControlMessage, previewReadyMessage } from './controls';
import {
  appearanceArt,
  dialogue,
  entries,
  entryName,
  introduction,
  turnBackIntroduction,
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
  const walking = entry.kind === 'tomorrow' && entry.walking;
  const poseStudy = entry.kind === 'tomorrow' && !walking && entry.poseStudy;
  const portraitStudy = entry.kind === 'tomorrow' && !walking && (entry.refined || poseStudy);
  const assets = walking
    ? walkingTomorrowAssets
    : poseStudy
      ? { ...refinedTomorrowAssets, ...turnBackTomorrowAssets }
      : portraitStudy
        ? refinedTomorrowAssets
        : tomorrowAssets;
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
  const files = portraitStudy
    ? [[t('完整卡面', 'Card'), entry.art]]
    : [
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
          entry.kind === 'tomorrow'
            ? t('深色装扮', 'Dark appearance')
            : t('深色背景', 'Dark scenery'),
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
                  {item.kind === 'tomorrow'
                    ? item.walking
                      ? t(' · 迈步版', ' · Walking pose')
                      : item.poseStudy
                        ? t(' · 站定版（静态对照）', ' · Standing pose (still reference)')
                        : item.refined
                          ? t(' · 伸手精修', ' · Refined reaching pose')
                          : t(' · 初版', ' · First version')
                    : ''}
                </option>
              ))}
            </select>
          )}
        </Field>
        <p>
          <span className={'sp-review__badge' + (entry.kind === 'tomorrow' ? ' is-candidate' : '')}>
            {entry.kind === 'tomorrow'
              ? walking
                ? t('迈步版', 'Walking pose')
                : poseStudy
                  ? t('站定版 · 静态对照', 'Standing pose · Still reference')
                  : portraitStudy
                    ? t('伸手精修', 'Refined reaching pose')
                    : t('初版', 'First version')
              : t('已发布', 'Published')}
          </span>
          <span>{english ? entry.noteEN : entry.note}</span>
        </p>
      </div>
      {portraitStudy && view !== 'card' && view !== 'materials' ? (
        <div className="sp-review__materials">
          <p>
            {poseStudy
              ? t(
                  '站定版只保留静态竖卡对照。封面、插画与明暗装扮可在迈步版中查看。',
                  'The standing pose remains as a still portrait reference. View the walking pose for covers, illustrations and appearances.',
                )
              : t(
                  '本轮先精修晨光竖卡。封面、插画与明暗装扮仍可在初版中查看。',
                  'This study refines the daylight portrait. The cover, illustration and appearances remain available in the first version.',
                )}
          </p>
          <Button onClick={() => setEntry(poseStudy ? 'tomorrow-walking' : 'tomorrow')}>
            {poseStudy
              ? t('查看迈步版的这项素材', 'View this material in the walking pose')
              : t('查看初版的这项素材', 'View this material in the first version')}
          </Button>
        </div>
      ) : view === 'card' ? (
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
          {portraitStudy && (
            <p className="sp-review__note">
              {poseStudy
                ? t(
                    '站定版 · 静态对照；明暗切换只改变预览界面。',
                    'Standing pose · Still reference; the theme switch changes the review interface.',
                  )
                : t(
                    '晨光竖卡试作 · 切换明暗只改变预览界面；其他成品尚未同步精修。',
                    'Daylight portrait study · The theme switch changes the review interface; other formats have not been refined yet.',
                  )}
            </p>
          )}
          {entry.kind === 'tomorrow' && (
            <div className="sp-review__dialogue">
              <p>{(walking || poseStudy ? turnBackIntroduction : introduction)[english ? 1 : 0]}</p>
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
                [t('人物', 'Character'), assets.hero],
                ...(!portraitStudy ? [[t('蓝调人物', 'Evening character'), assets.heroDark]] : []),
                ...(!poseStudy
                  ? [[t('闭眼校准图', 'Closed-eye registration'), assets.closed]]
                  : []),
                [t('晨光背景', 'Morning scenery'), assets.backgroundLight],
                ...(!portraitStudy
                  ? [[t('蓝调背景', 'Evening scenery'), assets.backgroundDark]]
                  : []),
                [t('云朵伙伴', 'Cloud companion'), assets.cloud],
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
