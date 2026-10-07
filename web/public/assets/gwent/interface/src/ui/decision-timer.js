/* eslint-disable */
const phaseNames = {
  turn: '你的回合',
  mulligan: '开局换牌',
  choice: '选择目标',
};

export function installDecisionTimer() {
  const create = (id, parent, before) => {
    const element = document.createElement('span');
    element.id = id;
    element.className = 'decision-timer';
    element.setAttribute('role', 'timer');
    element.setAttribute('aria-live', 'off');
    element.hidden = true;
    if (before) before.before(element);
    else parent.append(element);
    return element;
  };
  const elements = [
    create('decision-timer', document.querySelector('.battle-heading')),
    create('decision-dialog-timer', null, document.getElementById('dialog-cards')),
  ];
  return (state) => {
    for (const element of elements) {
      element.hidden = !state;
      if (!state) continue;
      const seconds = Math.ceil(state.remainingMs / 1000);
      element.textContent = `${phaseNames[state.phase] || '决策'} · ${seconds}s`;
      element.classList.toggle('urgent', seconds <= 5);
      element.setAttribute('aria-label', `剩余 ${seconds} 秒，超时自动处理`);
    }
  };
}
