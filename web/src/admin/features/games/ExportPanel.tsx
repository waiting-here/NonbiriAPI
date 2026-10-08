import { useGameAdminText, type GameID } from './copy';
import { historyDownloadURL } from './export';
import type { Dataset, Selection } from './history';

export function ExportPanel({
  game,
  dataset,
  selection = {},
}: {
  game: GameID | 'blackjack';
  dataset: Dataset;
  selection?: Selection;
}) {
  const t = useGameAdminText();
  const dated = selection.from !== undefined || selection.to !== undefined;
  return (
    <section className="admin-duel-export" aria-label={t('历史导出', 'History export')}>
      <h3>{t('下载历史数据', 'Download history')}</h3>
      <p>
        {t(
          '下载一个 ZIP，内含 UTF-8 NDJSON 数据和完整性清单。可在浏览器中查看进度或取消下载。',
          'Download one ZIP containing UTF-8 NDJSON records and a completion manifest. Your browser manages download progress and cancellation.',
        )}
      </p>
      {dataset === 'anonymous' && (
        <p>
          {dated
            ? t(
                '按所选时间范围导出仍保留日期的资料，下载内容会匿名化。长期档案已无日期，不包含在本次下载中。',
                'Records with retained dates in the selected range are anonymized for download. Older undated archives are excluded.',
              )
            : t(
                '下载包含全部长期档案及近期资料的匿名版本，不含账号、原局编号、绝对时间和付款来源。',
                'Includes all archived and recent records in anonymous form, without accounts, original match IDs, absolute times or payment sources.',
              )}
        </p>
      )}
      <a className="nb-btn nb-btn--primary" href={historyDownloadURL(game, dataset, selection)} download>
        {t('下载 ZIP', 'Download ZIP')}
      </a>
    </section>
  );
}
