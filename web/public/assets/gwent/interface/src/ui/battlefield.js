/* global document */
import { cardDescription, cardElement } from './card.js';
import { pulse } from './animation.js';
import { setTooltip } from './tooltip.js';

export class ArenaView {
  constructor(arena) {
    this.arena = arena;
    this.modalOpen = false;
    this.logItems = [];
    this.scores = new WeakMap();
  }
  log(text) {
    this.logItems.unshift(text);
    this.logItems = this.logItems.slice(0, 18);
    const list = document.getElementById('battle-log');
    list.replaceChildren(
      ...this.logItems.map((message) => {
        const item = document.createElement('li');
        item.textContent = message;
        return item;
      }),
    );
  }
  score(element, value) {
    const before = this.scores.get(element);
    element.textContent = value;
    this.scores.set(element, value);
    if (before !== undefined && before !== value) {
      const delta = value - before;
      element.dataset.change = `${delta > 0 ? '+' : ''}${delta}`;
      element.classList.toggle('score-down', delta < 0);
      setTooltip(element, `最近一次变化 ${element.dataset.change}`);
      pulse(element, { speed: this.arena.speed });
    }
  }
  preview(card) {
    const name = this.arena.label(card.holder);
    document.getElementById('action-title').textContent =
      `${name} ${card.row === 'leader' ? '激活领袖' : '打出'} · ${card.name}`;
    document.getElementById('action-description').textContent = cardDescription(card);
    const element = cardElement(card, { small: true });
    document.getElementById('action-card').replaceChildren(element);
    pulse(element, { kind: 'play', speed: this.arena.speed });
  }
  showDialog(title, description, actions) {
    this.modalOpen = true;
    document.getElementById('dialog-title').textContent = title;
    document.getElementById('dialog-description').textContent = description;
    document.getElementById('dialog-cards').replaceChildren();
    document.getElementById('dialog-actions').replaceChildren(
      ...actions.map(({ label, action }) => {
        const button = document.createElement('button');
        button.className = 'primary-button';
        button.textContent = label;
        button.onclick = action;
        return button;
      }),
    );
    const dialog = document.getElementById('arena-dialog');
    if (!dialog.open) dialog.showModal();
  }
  closeDialog() {
    document.getElementById('arena-dialog').close();
    this.modalOpen = false;
    this.render?.();
  }
}
