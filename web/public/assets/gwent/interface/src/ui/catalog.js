/* eslint-disable */
import { cardElement, cardDescription, abilityNames } from './card.js';
import { models } from '../ai/models.js';

const priority = { flagship: 100 };
export function sortCards(cards) {
  return [...cards].sort(
    (a, b) =>
      (priority[b.tier] || 0) - (priority[a.tier] || 0) ||
      b.power - a.power ||
      a.name.localeCompare(b.name),
  );
}
export function matchesCard(card, query, kind = 'all') {
  const text = [
    card.name,
    card.modelName,
    card.id,
    card.role,
    ...card.abilities.map((id) => abilityNames[id] || id),
  ]
    .join(' ')
    .toLocaleLowerCase();
  return (
    (!query.trim() || text.includes(query.trim().toLocaleLowerCase())) &&
    (kind === 'all' ||
      card.type === kind ||
      (kind === 'frontier' && ['flagship', 'frontier'].includes(card.tier)) ||
      (kind === 'special' && ['skill', 'weather'].includes(card.type)))
  );
}

export class CardCatalog {
  constructor(arena) {
    this.arena = arena;
    this.dialog = document.getElementById('card-catalog');
    const filters = ['catalog-query', 'catalog-faction', 'catalog-kind'];
    for (const id of filters)
      document
        .getElementById(id)
        .addEventListener(id === 'catalog-query' ? 'input' : 'change', () => this.draw());
    document.getElementById('catalog-close').onclick = () => this.close();
    this.dialog.addEventListener('cancel', (event) => {
      event.preventDefault();
      this.close();
    });
    document.getElementById('view-all-cards').onclick = () => {
      if (arena.view.modalOpen || document.getElementById('lobby').hidden) return;
      document.getElementById('catalog-query').value = '';
      document.getElementById('catalog-faction').value = 'all';
      document.getElementById('catalog-kind').value = 'all';
      this.draw();
      this.dialog.showModal();
      document.getElementById('catalog-query').focus();
    };
  }
  close() {
    this.dialog.close();
    document.getElementById('view-all-cards').focus();
  }
  draw() {
    const faction = document.getElementById('catalog-faction').value;
    const list = sortCards(this.arena.catalog).filter(
      (card) =>
        (faction === 'all' || card.faction === faction) &&
        matchesCard(
          card,
          document.getElementById('catalog-query').value,
          document.getElementById('catalog-kind').value,
        ),
    );
    document.getElementById('catalog-count').textContent =
      `显示 ${list.length} / ${this.arena.catalog.length} 种卡牌 · 旗舰优先，随后按战力排序`;
    document.getElementById('catalog-grid').replaceChildren(
      ...list.map((card) => {
        const article = document.createElement('article');
        article.className = 'deck-choice';
        const factionLabel = document.createElement('p');
        factionLabel.className = 'card-origin';
        factionLabel.textContent = models[card.faction]?.short || '中立';
        const details = document.createElement('details'),
          summary = document.createElement('summary'),
          description = document.createElement('p');
        summary.textContent = '技能与型号说明';
        description.textContent = cardDescription(card);
        details.append(summary, description);
        if (card.source?.startsWith('https://')) {
          const link = document.createElement('a');
          link.href = card.source;
          link.target = '_blank';
          link.rel = 'noopener noreferrer';
          link.textContent = '官方型号来源';
          details.append(link);
        }
        article.append(cardElement(card), factionLabel, details);
        return article;
      }),
    );
  }
}
