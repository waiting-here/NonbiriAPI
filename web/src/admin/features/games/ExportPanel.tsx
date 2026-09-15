import { useEffect, useRef, useState } from 'react';
import { ErrorState } from '@shared/components/States';
import { gameLabel, modeLabel, useGameAdminText, type GameID } from './copy';
import { HistoryExport, downloadPart, type ExportProgress } from './export';
import type { Dataset, Selection } from './history';

export function ExportPanel({
  game,
  dataset,
  selection,
}: {
  game: GameID;
  dataset: Dataset;
  selection: Selection;
}) {
  const t = useGameAdminText(),
    job = useRef<HistoryExport | null>(null),
    controller = useRef<AbortController | null>(null);
  const [progress, setProgress] = useState<ExportProgress | null>(null),
    [running, setRunning] = useState(false),
    [error, setError] = useState<unknown>(null);
  const [scope, setScope] = useState<{
    game: GameID;
    dataset: Dataset;
    selection: Selection;
  } | null>(null);
  useEffect(() => () => controller.current?.abort(), []);
  const run = async (fresh: boolean) => {
    if (controller.current) return;
    if (fresh || !job.current) {
      job.current = new HistoryExport(game, dataset, selection);
      setProgress(job.current.progress);
      setScope({ game, dataset, selection: { ...selection } });
    }
    const current = job.current,
      abort = new AbortController();
    controller.current = abort;
    setError(null);
    setRunning(true);
    try {
      while (!current.progress.complete)
        setProgress(await current.step(abort.signal, downloadPart));
    } catch (failure) {
      if (!abort.signal.aborted) setError(failure);
    } finally {
      controller.current = null;
      setRunning(false);
    }
  };
  return (
    <section className="admin-duel-export" aria-label={t('历史导出', 'History export')}>
      <h3>{t('按页下载完整过程', 'Download complete records in pages')}</h3>
      <p>
        {t(
          '下载为UTF-8 NDJSON分片，每片最多16MiB。请允许本站下载多个文件；只有读取到最后一页才会显示完成。取消或失败时，已下载分片保留，可继续未完成的分页。',
          'Downloads use UTF-8 NDJSON parts, up to 16 MiB each. Allow multiple downloads from this site. Completion appears only after the last page. Cancelled or failed exports keep downloaded parts and can resume at the saved page.',
        )}
      </p>
      {progress && scope && (
        <p>
          {gameLabel(scope.game, t)} ·{' '}
          {scope.dataset === 'recent'
            ? t('近30天', 'Recent 30 days')
            : t('匿名资料', 'Anonymous records')}{' '}
          · {scope.selection.mode ? modeLabel(scope.selection.mode, t) : t('全部模式', 'All modes')}
          {' · '}
          {t('规则版本', 'Rules version')}: {scope.selection.rules_version ?? t('全部', 'All')}
          {' · '}
          {{
            normal: t('分出胜负', 'Decided'),
            draw: t('平局', 'Draw'),
            system_cancelled: t('系统取消', 'System cancelled'),
          }[scope.selection.outcome ?? ''] ?? t('全部结果', 'All outcomes')}
          {scope.selection.from !== undefined && (
            <>
              {' '}
              · {t('从', 'From')} {new Date(scope.selection.from * 1000).toLocaleString()}
            </>
          )}
          {scope.selection.to !== undefined && (
            <>
              {' '}
              · {t('至', 'Through')} {new Date(scope.selection.to * 1000).toLocaleString()}
            </>
          )}
        </p>
      )}
      <div role="status" aria-live="polite">
        {progress && (
          <>
            <strong>
              {progress.complete
                ? t('导出完成', 'Export complete')
                : running
                  ? t('正在导出', 'Exporting')
                  : t('导出尚未完成', 'Export incomplete')}
            </strong>
            <p>
              {t('已提供下载分片', 'Download parts provided')}: {progress.parts} ·{' '}
              {t('对局', 'Matches')}: {progress.matches} · {t('轮次', 'Rounds')}: {progress.rounds}{' '}
              · {t('跳过已到期对局', 'Expired matches skipped')}: {progress.expired}
            </p>
          </>
        )}
      </div>
      {error ? <ErrorState error={error} /> : null}
      <div className="ops-actions">
        {!running && (
          <button className="btn btn-primary" type="button" onClick={() => void run(true)}>
            {progress
              ? t('重新开始导出', 'Start a new export')
              : t('导出筛选结果', 'Export filtered results')}
          </button>
        )}
        {running ? (
          <button
            className="btn btn-secondary"
            type="button"
            onClick={() => controller.current?.abort()}
          >
            {t('取消导出', 'Cancel export')}
          </button>
        ) : progress && !progress.complete ? (
          <button className="btn btn-secondary" type="button" onClick={() => void run(false)}>
            {t('继续未完成导出', 'Resume incomplete export')}
          </button>
        ) : null}
      </div>
    </section>
  );
}
