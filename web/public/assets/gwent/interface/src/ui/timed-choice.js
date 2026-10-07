/* eslint-disable */
import { ROWS, rowNames } from '../game/rules.js';
import { cardElement, cardDescription } from './card.js';

function timedPhase(view, count, container) {
  if (game.state !== GameState.PLAYING || view.arena.mode !== 'play') return null;
  if (container?.choicePhase === 'initiative') return 'choice';
  if (game.roundCount === 0) return 'mulligan';
  if (!count && view.arena.clock.phase === 'turn') return null; // Reading consumes the existing turn budget.
  return 'choice';
}

export function chooseCardsTimed(view, container, count, action, predicate, canQuit, title) {
  return new Promise((resolve) => {
    const arena = view.arena,
      matchId = arena.matchId;
    const phase = timedPhase(view, count, container);
    const deadline =
      Date.now() + (phase === 'mulligan' ? arena.timing.mulliganMs : arena.timing.choiceMs);
    let remaining = count,
      busy = false,
      settled = false,
      token;
    const request = { expire: () => finish() };
    const finish = () => {
      if (settled) return;
      settled = true;
      if (token !== undefined) arena.clock.stop(token);
      if (view.pendingDecision === request) view.pendingDecision = null;
      view.closeDialog();
      resolve();
    };
    const options = () =>
      container.cards
        .map((card, index) => ({ card, index }))
        .filter(({ card }) => !predicate || predicate(card));
    const pick = async (index, automatic = false) => {
      if (settled || busy || !count || arena.matchId !== matchId) return;
      if (!automatic && token !== undefined && !arena.clock.claim(token)) return;
      busy = true;
      document
        .querySelectorAll('#dialog-cards button, #dialog-actions button')
        .forEach((button) => {
          button.disabled = true;
        });
      try {
        await action(container, index);
        remaining--;
        busy = false;
        draw();
      } catch (error) {
        finish();
        arena.fail(error);
      }
    };
    const expire = () => {
      if (settled || busy || arena.matchId !== matchId || arena.finished) return;
      if (canQuit || !count) {
        arena.view.log(
          phase === 'mulligan'
            ? '开局换牌时间已到 · 保留当前手牌，继续对局。'
            : '选择时间已到 · 继续对局。',
        );
        finish();
      } else {
        const valid = options();
        if (!valid.length) {
          finish();
          return;
        }
        arena.view.log('选择目标已超时 · 自动选择一个合法目标。');
        pick(valid[0].index, true);
      }
    };
    request.expire = expire;
    const draw = () => {
      if (settled) return;
      const valid = options();
      if (!valid.length || (count && remaining <= 0)) {
        finish();
        return;
      }
      view.showDialog(
        title,
        count ? `还可选择 ${remaining} 张${canQuit ? '，也可以直接继续' : ''}` : '卡牌信息',
        [],
      );
      view.pendingDecision = request;
      document.getElementById('dialog-cards').replaceChildren(
        ...valid.map(({ card, index }) => {
          const wrapper = document.createElement('div');
          wrapper.className = 'dialog-card';
          const element = cardElement(card, { onClick: () => pick(index) });
          const desc = document.createElement('p');
          desc.textContent = cardDescription(card);
          wrapper.append(element, desc);
          return wrapper;
        }),
      );
      if (canQuit || !count) {
        const button = document.createElement('button');
        button.className = 'primary-button';
        button.textContent = count ? '继续对局' : '关闭';
        button.onclick = () => {
          if (busy || settled) return;
          if (token !== undefined && !arena.clock.claim(token)) return;
          finish();
        };
        document.getElementById('dialog-actions').append(button);
        button.focus();
      }
      if (phase) token = arena.clock.arm(phase, Math.max(0, deadline - Date.now()), expire);
    };
    draw();
  });
}

export function chooseRowTimed(view, card) {
  return new Promise((resolve) => {
    const arena = view.arena,
      matchId = arena.matchId;
    const choices = ROWS.filter((row) =>
      card.row === 'agile' ? row !== 'siege' : row === card.row,
    );
    let settled = false,
      token;
    const finish = (row, automatic = false) => {
      if (settled || arena.matchId !== matchId) return;
      if (!automatic && token !== undefined && !arena.clock.claim(token)) return;
      settled = true;
      if (token !== undefined) arena.clock.stop(token);
      view.pendingDecision = null;
      view.closeDialog();
      resolve(board.getRow(card, row, card.holder));
    };
    view.showDialog(
      '选择部署战线',
      card.name,
      choices.map((row) => ({
        label: rowNames[row],
        action: () => finish(row),
      })),
    );
    const expire = () => {
      arena.view.log('选择战线已超时 · 自动部署到合法战线。');
      finish(choices[0], true);
    };
    view.pendingDecision = { expire };
    if (timedPhase(view, 1)) token = arena.clock.arm('choice', arena.timing.choiceMs, expire);
  });
}

export function expireDialog(view) {
  if (view.pendingDecision) view.pendingDecision.expire();
  else if (view.modalOpen) view.closeDialog();
  for (const dialog of document.querySelectorAll('dialog[open]')) dialog.close();
  view.modalOpen = false;
}
