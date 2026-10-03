import { useEffect, useRef, useState } from 'react';
import { Fold, Note } from '@shared/components/ui';
import { useTranslation } from 'react-i18next';
import { useOptionalToast } from '@shared/components/Toast';
import { useGameCopy } from '../copy';
import { formatCredits, sumCredits } from './strict';
import type { OnboardingGameID, OnboardingProgress, OnboardingTaskKey } from './types';

const taskCopy = {
  worm: 'fishing.bait.worm',
  lure: 'fishing.bait.lure',
  premium: 'fishing.bait.premium',
  '6x8': 'onboarding.board6x8',
  '8x8': 'onboarding.board8x8',
  '10x10': 'onboarding.board10x10',
  quick: 'rps.mode.quick',
  standard: 'rps.mode.standard',
  deathmatch: 'rps.mode.deathmatch',
  complete_tier_1: 'onboarding.tier1',
  complete_tier_2: 'onboarding.tier2',
  complete_tier_3: 'onboarding.tier3',
  first_win: 'onboarding.firstWin',
  quick_complete: 'onboarding.quickComplete',
  quick_win: 'onboarding.quickWin',
  standard_complete: 'onboarding.standardComplete',
  standard_win: 'onboarding.standardWin',
  complete: 'onboarding.complete',
  first_bust: 'onboarding.firstBust',
  first_21: 'onboarding.first21',
  first_natural_21: 'onboarding.firstNatural21',
} as const satisfies Record<OnboardingTaskKey, string>;

export function OnboardingCard({
  game,
  progress,
  compactNote = false,
}: {
  readonly game: OnboardingGameID;
  readonly progress: OnboardingProgress;
  readonly compactNote?: boolean;
}) {
  const { text } = useGameCopy();
  const { t } = useTranslation();
  const [dismissed, setDismissed] = useState(false);
  const previous = useRef(progress);
  const pushToast = useOptionalToast()?.push;
  useEffect(() => {
    for (const item of progress.items) {
      if (
        item.completed &&
        previous.current.items.some((old) => old.key === item.key && !old.completed)
      ) {
        pushToast?.({
          tone: 'success',
          title: text('onboarding.awarded', { reward: formatCredits(item.reward) }),
          message: text(taskCopy[item.key]),
        });
      }
    }
    previous.current = progress;
  }, [progress, pushToast, text]);

  if (progress.allCompleted) return null;
  const pending = progress.items.filter((item) => !item.completed);
  const summary = text('onboarding.remaining', {
    count: pending.length,
    reward: formatCredits(sumCredits(pending.map((item) => item.reward))),
  });
  return (
    <div className={`game-onboarding${compactNote ? ' game-onboarding--compact' : ''}`}>
      <Fold title={text('onboarding.title')} summary={summary}>
        <p>{text(`onboarding.${game}Help`)}</p>
        <ul>
          {pending.map((item) => (
            <li key={item.key}>
              <span>{text(taskCopy[item.key])}</span>
              <span>
                <strong>+{formatCredits(item.reward)}</strong> {text('common.generalBalance')}
              </span>
            </li>
          ))}
        </ul>
      </Fold>
      {compactNote && !dismissed ? (
        <div className="game-onboarding__note">
          <Note
            title={text('onboarding.title')}
            action={
              <button
                type="button"
                className="btn btn-quiet"
                aria-label={t('common.close')}
                onClick={() => setDismissed(true)}
              >
                ×
              </button>
            }
          >
            {summary}
          </Note>
        </div>
      ) : null}
    </div>
  );
}
