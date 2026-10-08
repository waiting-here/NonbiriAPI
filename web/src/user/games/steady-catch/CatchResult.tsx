import type { CatchFeedback } from './Board';
import { HZ } from './engine';
import type { Session } from './session';
import { useCatchText } from './copy';
import { formatCredits } from '../common/strict';

export function CatchResult({
  result,
  previousBest,
  caught,
  disabled,
  busy,
  start,
  openCatalog,
}: {
  result: Session;
  previousBest: number | null;
  caught: readonly CatchFeedback[];
  disabled: boolean;
  busy: boolean;
  start: () => void;
  openCatalog: () => void;
}) {
  const t = useCatchText();
  const state = result.state;
  const scored = result.status === 'completed' || result.status === 'failed';
  const improvement = scored && previousBest !== null ? state.score - previousBest : 0;
  const memorable =
    caught.find((phrase) => phrase.gold) ??
    caught.reduce<CatchFeedback | undefined>(
      (best, phrase) => (!best || phrase.combo > best.combo ? phrase : best),
      undefined,
    );
  return (
    <div className={improvement > 0 ? 'catch-result new-record' : 'catch-result'}>
      {improvement > 0 && (
        <p className="record-badge">
          {t('新纪录！比之前高 +', 'New record! Up by +')}
          {improvement}
        </p>
      )}
      <p className="result-message">
        {t('坚持了 ', 'Lasted ')}
        {Math.ceil(state.tick / HZ)}
        {t(' 秒', ' seconds')}
      </p>
      <div className="end-score">
        {state.score} <span>{t('分', 'points')}</span>
      </div>
      {result.first_clear && (
        <p className="first-clear-badge">
          {t('首次通关！获得游戏积分：', 'First clear! Game credits awarded: ')}
          {formatCredits(result.reward)}
        </p>
      )}
      <div className="result-stats">
        <span>
          <b>{state.caught}</b>
          {t('接住句数', 'Caught')}
        </span>
        <span>
          <b>{state.max_combo}</b>
          {t('最高连击', 'Best combo')}
        </span>
        <span>
          <b>{state.missed}</b>
          {t('漏接', 'Missed')}
        </span>
      </div>
      {memorable && (
        <blockquote className="memorable-phrase">
          <small>{t('本局名场面', 'A memorable catch')}</small>“{memorable.text}”
        </blockquote>
      )}
      <button className="primary" disabled={disabled} onClick={start}>
        {busy ? t('正在开局', 'Starting') : t('再接一局', 'Catch again')}
      </button>
      <button className="secondary" onClick={openCatalog}>
        {t('看本局接住的梗', 'See this round’s catches')}
      </button>
    </div>
  );
}
