/* eslint-disable */
import { cardDescription, definition } from './card.js';
import { models } from '../ai/models.js';
import { setTooltip, showTooltip } from './tooltip.js';

// Keep inspection available even when the action itself is disabled.
export function renderLeaderHelp(leader, available) {
  const action = document.getElementById('arena-leader');
  let control = action.closest('.leader-control');
  if (!control) {
    control = document.createElement('span');
    control.className = 'leader-control';
    action.before(control);
    control.append(action);
    const help = document.createElement('button');
    help.id = 'arena-leader-help';
    help.type = 'button';
    help.className = 'secondary-button leader-help';
    help.textContent = 'ⓘ';
    help.setAttribute('aria-label', '查看领袖技能说明');
    help.onclick = () => showTooltip(help);
    control.append(help);
  }
  const data = definition(leader);
  const passive = leader.abilities.includes('leader_deepseek_rebirth');
  const usage = passive
    ? '被动自动触发，无需点击领袖按钮。'
    : '使用会占用一次行动；换小局不会重置使用次数。';
  const status = passive
    ? '当前为被动领袖。'
    : available
      ? '领袖尚未使用，需己方行动时使用。'
      : '领袖已使用或被封锁，本场不能再次使用。';
  const text = `${data.name}\n${cardDescription(leader)}\n${usage}\n${status}`;
  control.style.setProperty('--card-color', models[data.faction]?.color || '#77e4c8');
  for (const element of [control, action, control.querySelector('.leader-help')]) {
    element.style.setProperty('--card-color', models[data.faction]?.color || '#77e4c8');
    setTooltip(element, text);
  }
}
