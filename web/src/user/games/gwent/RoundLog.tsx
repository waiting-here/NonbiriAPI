import type { DuelRound, Seat } from '../common/duel/types';
import { rowName, useGwentText } from './copy';
import type { Action, Round, View } from './types';

export function RoundLog({ round, you }: { round: DuelRound<View, Round, never>; you: Seat }) {
  const t = useGwentText();
  const cards = new Map(
    [round.before, round.after]
      .flatMap((view) => [
        ...view.hand,
        ...view.weather,
        view.self.leader,
        view.enemy.leader,
        ...view.self.grave,
        ...view.enemy.grave,
        ...view.board.flatMap((row) => [...row.cards, ...(row.special ? [row.special] : [])]),
      ])
      .map((card) => [card.instance_id, card.name]),
  );
  const describe = (action: Action) => {
    const verb =
      action.kind === 'play'
        ? t('出牌', 'Play')
        : action.kind === 'leader'
          ? t('领袖技能', 'Leader ability')
          : action.kind === 'pass'
            ? t('放弃本小局', 'Pass')
            : action.kind === 'mulligan'
              ? t('换牌', 'Replace')
              : action.kind === 'continue'
                ? t('继续', 'Continue')
                : t('选择', 'Choose');
    return [
      verb,
      action.card === undefined ? '' : (cards.get(action.card) ?? t('卡牌', 'Card')),
      action.row ? rowName(action.row, t) : '',
    ]
      .filter(Boolean)
      .join(' · ');
  };
  return (
    <div>
      <p>
        {round.facts.scores[you]} : {round.facts.scores[1 - you]}
      </p>
      <ol className="gwt-action-log">
        {round.facts.actions?.map((step, index) => (
          <li key={index}>
            <strong>{step.seat === you ? t('你', 'You') : t('对手', 'Opponent')}</strong>
            {' · '}
            {describe(step.action)}
            {step.automatic ? ` · ${t('超时托管', 'Automatic')}` : ''}
          </li>
        ))}
      </ol>
    </div>
  );
}
