/* eslint-disable */
import { models } from '../ai/models.js';

export function setFactionMark(element, faction) {
  const model = models[faction];
  if (!model) return;
  if (element.dataset.logoFaction === faction) return;
  const logo = document.createElement('span');
  logo.className = 'faction-logo';
  logo.style.setProperty(
    '--faction-logo',
    `url("${new URL(`../../assets/factions/${faction}.svg`, import.meta.url).href}")`,
  );
  logo.setAttribute('aria-hidden', 'true');
  element.replaceChildren(logo);
  element.dataset.logoFaction = faction;
  element.setAttribute('role', 'img');
  element.setAttribute('aria-label', `${model.short} 阵营标志`);
}
