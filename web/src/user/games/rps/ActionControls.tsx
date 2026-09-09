import { useState } from 'react';
import { useGameCopy } from '../copy';
import { creditsFromMilli, creditsToMilli, formatCredits } from '../common/strict';
import { GestureArt } from './GestureArt';
import { GESTURES, type RPSActionPayload, type RPSState } from './types';

export function ActionControls({
  state,
  busy,
  current,
  onAction,
  onSelection,
}: {
  readonly state: RPSState;
  readonly busy: boolean;
  readonly current: boolean;
  readonly onAction: (value: RPSActionPayload) => void;
  readonly onSelection?: () => void;
}) {
  const { text } = useGameCopy();
  const option = state.currentActorOptions[0];
  const [draft, setDraft] = useState({ value: '1', change: 0, maximum: false });
  const maximumMilli =
    state.seats.reduce<bigint | null>((minimum, seat) => {
      const value = creditsToMilli(seat.currentBalance);
      return minimum === null || value < minimum ? value : minimum;
    }, null) ?? 0n;
  const maximumWhole = maximumMilli / 1000n;
  const base = creditsToMilli(state.ruleSnapshot.base);
  const own = state.seats.find((seat) => seat.viewer === 'self');
  const allIn = own && creditsToMilli(own.currentBalance) === maximumWhole * 1000n;
  const raiseValid =
    draft.value.length <= 75 &&
    /^[1-9][0-9]*$/.test(draft.value) &&
    BigInt(draft.value) <= maximumWhole;
  const blocked = busy || !current;
  const adjust = (change: bigint | 'maximum') => {
    if (blocked) return;
    const [whole, fraction = ''] = draft.value.split('.');
    const original =
      draft.value.length <= 75 && /^[0-9]+(?:\.[0-9]{1,3})?$/.test(draft.value)
        ? BigInt(whole) * 1000n + BigInt(fraction.padEnd(3, '0'))
        : 0n;
    const limit = maximumWhole * 1000n;
    const next = change === 'maximum' ? limit : original + change;
    const clamped = next < 0n ? 0n : next > limit ? limit : next;
    setDraft({
      value: creditsFromMilli(clamped),
      change: draft.change + 1,
      maximum: change === 'maximum',
    });
    onSelection?.();
  };
  if (!option) return <p className="game-inline-notice">{text('rps.waiting')}</p>;
  if (option === 'gesture')
    return (
      <div className="rps-actions" aria-label={text(`rps.phase.${state.phase}`)}>
        {GESTURES.map((gesture) => (
          <button
            type="button"
            key={gesture}
            disabled={blocked}
            onClick={() => onAction({ action: 'gesture', payload: { gesture } })}
          >
            <GestureArt gesture={gesture} />
            <span>{text(`rps.gesture.${gesture}`)}</span>
          </button>
        ))}
      </div>
    );
  if (option === 'dealer_decision')
    return (
      <div className="rps-dealer">
        <p>
          {maximumWhole > 0n
            ? text('rps.dealer.integerOnly', { max: maximumWhole.toString() })
            : text('rps.dealer.noCapacity')}
        </p>
        <div className="game-state-actions">
          <button
            type="button"
            className="btn btn-secondary"
            disabled={blocked}
            onClick={() =>
              onAction({ action: 'dealer_decision', payload: { decision: 'no_raise' } })
            }
          >
            {text('rps.dealer.noRaise')}
          </button>
          {maximumWhole > 0n ? (
            <>
              <label className="rps-raise-input">
                <span>{text('rps.raiseAmount')}</span>
                <input
                  value={draft.value}
                  inputMode="numeric"
                  pattern="[1-9][0-9]*"
                  maxLength={75}
                  disabled={blocked}
                  onChange={(event) => setDraft({ ...draft, value: event.target.value })}
                />
                {draft.change > 0 ? (
                  <i
                    key={draft.change}
                    aria-hidden="true"
                    className={`rps-raise-flash${draft.maximum ? ' is-maximum' : ''}`}
                  />
                ) : null}
              </label>
              <div
                className="rps-raise-shortcuts"
                role="group"
                aria-label={text('rps.dealer.adjust')}
              >
                <button
                  type="button"
                  className="btn btn-secondary"
                  disabled={blocked}
                  onClick={() => adjust(-base)}
                >
                  {text('rps.dealer.subtract', { amount: formatCredits(state.ruleSnapshot.base) })}
                </button>
                <button
                  type="button"
                  className="btn btn-secondary"
                  disabled={blocked}
                  onClick={() => adjust(base)}
                >
                  {text('rps.dealer.add', { amount: formatCredits(state.ruleSnapshot.base) })}
                </button>
                <button
                  type="button"
                  className="btn btn-secondary"
                  disabled={blocked}
                  onClick={() => adjust('maximum')}
                >
                  {text(allIn ? 'rps.dealer.allIn' : 'rps.dealer.maximum', {
                    amount: formatCredits(maximumWhole.toString()),
                  })}
                </button>
              </div>
              <button
                type="button"
                className="btn btn-primary"
                disabled={blocked || !raiseValid}
                onClick={() =>
                  onAction({
                    action: 'dealer_decision',
                    payload: { decision: 'raise', amount: draft.value },
                  })
                }
              >
                {text('rps.dealer.raise')}
              </button>
            </>
          ) : null}
        </div>
      </div>
    );
  const callKnown = current && state.economy.dealerRaise !== null;
  return (
    <div className="rps-follower">
      <p role="status">
        {callKnown
          ? text('rps.follower.required', {
              amount: formatCredits(state.economy.dealerRaise!),
            })
          : text('rps.waitingSync')}
      </p>
      <div className="game-state-actions">
        <button
          type="button"
          className="btn btn-primary"
          disabled={blocked || !callKnown}
          onClick={() => onAction({ action: 'follower_decision', payload: { decision: 'call' } })}
        >
          {text('rps.follower.call')}
        </button>
        <button
          type="button"
          className="btn btn-secondary"
          disabled={blocked || !callKnown}
          onClick={() =>
            onAction({ action: 'follower_decision', payload: { decision: 'surrender' } })
          }
        >
          {text('rps.follower.surrender')}
        </button>
      </div>
    </div>
  );
}
