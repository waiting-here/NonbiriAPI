import { useEffect, useId, useRef, useState } from 'react';
import { Card } from '@shared/components/States';
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
} as const satisfies Record<OnboardingTaskKey, string>;

export function OnboardingCard({
  game,
  progress,
}: {
  readonly game: OnboardingGameID;
  readonly progress: OnboardingProgress;
}) {
  const { text } = useGameCopy();
  const [expanded, setExpanded] = useState(true);
  const listID = useId();
  const previous = useRef(progress);
  const pushToast = useOptionalToast()?.push;
  useEffect(() => {
    for (const item of progress.items) {
      if (item.completed && previous.current.items.some((old) => old.key === item.key && !old.completed)) {
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
  return (
    <Card className="game-onboarding">
      <h2>
        <button
          type="button"
          aria-expanded={expanded}
          aria-controls={listID}
          onClick={() => setExpanded((value) => !value)}
        >
          <span>{text('onboarding.title')}</span>
          <span>{text('onboarding.remaining', { count: pending.length, reward: formatCredits(sumCredits(pending.map((item) => item.reward))) })}</span>
          <span aria-hidden="true">{expanded ? '−' : '+'}</span>
        </button>
      </h2>
      <div id={listID} hidden={!expanded}>
        <p>{text(`onboarding.${game}Help`)}</p>
        <ul>
          {pending.map((item) => (
            <li key={item.key}>
              <span>{text(taskCopy[item.key])}</span>
              <span><strong>+{formatCredits(item.reward)}</strong> {text('common.generalBalance')}</span>
            </li>
          ))}
        </ul>
      </div>
    </Card>
  );
}
