import { useEffect, useRef, useState } from 'react';
import { useQueryClient } from '@tanstack/react-query';
import { ErrorState } from '@shared/components/States';
import { useDateTimeFormatter } from '@shared/utils/datetime';
import { usePictureBookText } from '@shared/picturebook/copy';
import { getImage } from '@shared/picturebook/publicApi';
import type { ImageInfo, ImageTask } from '@shared/picturebook/publicTypes';
import { economySessionRequest } from '../economy/queries';

interface LocalImage {
  info: ImageInfo;
  url: string;
}
export function ImageResults({
  task,
  account,
}: {
  readonly task: ImageTask;
  readonly account: string;
}) {
  const formatDateTime = useDateTimeFormatter();
  const t = usePictureBookText(),
    client = useQueryClient();
  const [images, setImages] = useState<LocalImage[]>([]),
    [loading, setLoading] = useState(task.result_available),
    [retry, setRetry] = useState(0),
    [error, setError] = useState<unknown>(null);
  const stored = useRef<LocalImage[]>([]),
    controller = useRef<AbortController | null>(null),
    mounted = useRef(true);
  useEffect(() => {
    mounted.current = true;
    return () => {
      mounted.current = false;
      controller.current?.abort();
      for (const image of stored.current) URL.revokeObjectURL(image.url);
      stored.current = [];
    };
  }, []);
  const manifest = task.result_available ? JSON.stringify(task.images) : '';
  const taskID = task.id;
  useEffect(() => {
    if (!manifest) return;
    const current = new AbortController();
    controller.current = current;
    const collect = async () => {
      try {
        for (const info of JSON.parse(manifest) as ImageInfo[]) {
          if (stored.current.some((image) => image.info.index === info.index)) continue;
          const blob = await economySessionRequest(
            client,
            () => getImage(taskID, info, current.signal),
            account,
          );
          if (!mounted.current || current.signal.aborted) return;
          const url = URL.createObjectURL(blob);
          stored.current = [...stored.current, { info, url }];
          setImages(stored.current);
        }
      } catch (caught) {
        if (mounted.current && !current.signal.aborted) setError(caught);
      } finally {
        if (mounted.current && controller.current === current) setLoading(false);
      }
    };
    void collect();
    return () => current.abort();
  }, [account, client, manifest, retry, taskID]);
  const retryCollection = () => {
    setError(null);
    setLoading(true);
    setRetry((value) => value + 1);
  };
  return (
    <section aria-label={t('生成图片', 'Generated images')}>
      <p>
        {t('实际生成', 'Generated')}: {task.actual_images} / {task.n}
      </p>
      {task.actual_images > 0 && task.actual_images < task.n ? (
        <p>
          {t(
            '本次为部分成功，按提交时的全部费用收费。',
            'This task partially succeeded and is charged the full submitted price.',
          )}
        </p>
      ) : null}
      {task.result_available && images.length < task.images.length ? (
        <button className="btn btn-primary" disabled={loading} onClick={retryCollection}>
          {loading
            ? t('正在领取图片', 'Collecting images')
            : t('领取并预览原图', 'Collect and preview originals')}
        </button>
      ) : null}
      {!task.result_available ? (
        <p role="status">
          {images.length
            ? t(
                '服务器领取窗口已结束，当前页面中已领取的图片仍可下载。离开此页后无法恢复。',
                'The server collection window has ended. Images already collected on this page remain downloadable until you leave.',
              )
            : t(
                '图片已无法领取，可能已过期或因服务重启丢失。成功生成的费用不予退还。',
                'Images are no longer available, possibly because the collection window expired or the service restarted. Successful generation is not refunded.',
              )}
        </p>
      ) : null}
      {task.result_expires_at !== null && task.result_available ? (
        <p>
          {t('领取截止', 'Collect by')}: {formatDateTime(task.result_expires_at)} ·{' '}
          {t('本地时间', 'Local time')}
        </p>
      ) : null}
      {error ? (
        <ErrorState error={error} onRetry={task.result_available ? retryCollection : undefined} />
      ) : null}
      <div className="picturebook-grid picturebook-images">
        {images.map(({ info, url }) => (
          <figure key={info.index}>
            <img src={url} alt={t('生成图片 ', 'Generated image ') + (info.index + 1)} />
            <figcaption>
              <span>
                {t('图片 ', 'Image ') + (info.index + 1)} · {(info.bytes / 1024 / 1024).toFixed(2)}{' '}
                MiB
              </span>
              <a
                className="btn btn-secondary"
                href={url}
                download={
                  'picture-' +
                  task.id +
                  '-' +
                  (info.index + 1) +
                  (info.mime === 'image/jpeg'
                    ? '.jpg'
                    : info.mime === 'image/webp'
                      ? '.webp'
                      : '.png')
                }
              >
                {t('下载原图', 'Download original')}
              </a>
            </figcaption>
          </figure>
        ))}
      </div>
    </section>
  );
}
