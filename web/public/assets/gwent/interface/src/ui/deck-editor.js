/* eslint-disable */
import { DEFAULT_PRESET, exportDeck, importDeck, MAX_PROFILES } from '../cards/decks.js';
import { validateDeck, validateStoredDeck, heroCount, MAX_HEROES } from '../game/rules.js';
import { models } from '../ai/models.js';
import { cardElement, cardDescription } from './card.js';
import { sortCards, matchesCard } from './catalog.js';
import { setTooltip, showTooltip } from './tooltip.js';
import { isBuiltInSelection, isRandomSelection } from '../cards/presets.js';
import { leaderDefinitions } from '../cards/leaders.js';

export class DeckEditor {
  constructor(arena, select) {
    this.arena = arena;
    this.select = select;
    this.dialog = document.getElementById('deck-editor');
    this.dialog.querySelector('.deck-editor-note').prepend(`每套最多 ${MAX_HEROES} 张英雄；`);
    this.saveButton = document.getElementById('deck-save');
    this.nameInput = document.getElementById('deck-name');
    this.notice = document.getElementById('deck-notice');
    this.leaderPicker = document.createElement('select');
    this.leaderPicker.id = 'deck-leader-select';
    this.leaderPicker.setAttribute('aria-label', '选择领袖方案');
    const leaderLabel = document.createElement('label');
    leaderLabel.className = 'leader-picker';
    leaderLabel.append('领袖方案', this.leaderPicker);
    this.leaderHelp = document.createElement('button');
    this.leaderHelp.type = 'button';
    this.leaderHelp.className = 'deck-help';
    this.leaderHelp.textContent = 'ⓘ 技能说明';
    this.leaderHelp.onclick = () => showTooltip(this.leaderHelp);
    leaderLabel.append(this.leaderHelp);
    document.getElementById('deck-leader').replaceChildren(leaderLabel);
    this.leaderPicker.onchange = () => {
      this.draft.leader = this.leaderPicker.value;
      this.drawLeader();
      this.refresh();
    };
    for (const side of ['player', 'opponent']) {
      const picker = document.getElementById(`${side}-deck`);
      const info = document.createElement('div');
      info.className = 'deck-plan';
      info.id = `${side}-deck-info`;
      info.setAttribute('aria-live', 'polite');
      const reroll = document.createElement('button');
      reroll.id = `${side}-deck-reroll`;
      reroll.type = 'button';
      reroll.className = 'ghost-button deck-reroll';
      reroll.textContent = '↻ 重新随机';
      reroll.onclick = () => {
        try {
          arena.decks.reroll(document.getElementById(`${side}-faction`).value, picker.value, side);
          this.updateSummary();
        } catch (error) {
          document.getElementById('deck-summary').textContent = error.message;
        }
      };
      const help = document.createElement('button');
      help.id = `${side}-deck-help`;
      help.type = 'button';
      help.className = 'deck-help';
      help.textContent = 'ⓘ 卡组说明';
      help.onclick = () => showTooltip(help);
      info.append(help, reroll);
      picker.closest('label').after(info);
    }
    document.getElementById('opponent-deck').onchange = () => this.updateSummary();
    this.nameInput.oninput = () => this.refresh();
    for (const id of ['deck-query', 'deck-kind', 'deck-selected'])
      document
        .getElementById(id)
        .addEventListener(id === 'deck-query' ? 'input' : 'change', () => this.filter());
    document.getElementById('player-deck').onchange = (event) => {
      try {
        arena.decks.select(select.value, event.target.value);
        this.updateSummary();
      } catch (error) {
        this.updateSummary();
        document.getElementById('deck-summary').textContent += ` · 切换失败：${error.message}`;
      }
    };
    document.getElementById('deck-new').onclick = () => {
      this.profileId = null;
      const names = new Set(arena.decks.list(select.value).map((profile) => profile.name));
      let number = 1;
      while (names.has(`卡组 ${number}`)) number++;
      this.nameInput.value = `卡组 ${number}`;
      this.notice.textContent = '已复制当前草稿，保存后创建新卡组。';
      this.draw();
    };
    document.getElementById('deck-delete').onclick = () => {
      try {
        arena.decks.remove(select.value, this.profileId);
        this.updateSummary();
        this.close();
      } catch (error) {
        this.notice.textContent = error.message;
      }
    };
    document.getElementById('deck-export').onclick = () => {
      try {
        const json = exportDeck(this.draft, this.nameInput.value, arena.catalog);
        const url = URL.createObjectURL(new Blob([json], { type: 'application/json' }));
        const link = document.createElement('a');
        link.href = url;
        link.download = `AI-Gwent-${this.draft.faction}-deck.json`;
        link.click();
        setTimeout(() => URL.revokeObjectURL(url), 1000);
        this.notice.textContent = '卡组已导出。';
      } catch (error) {
        this.notice.textContent = error.message;
      }
    };
    const fileInput = document.getElementById('deck-file');
    document.getElementById('deck-import').onclick = () => fileInput.click();
    fileInput.onchange = async () => {
      const file = fileInput.files[0],
        draft = this.draft;
      if (!file || !draft) return;
      try {
        if (file.size > 65536) throw Error('卡组文件不能超过 64 KB');
        const imported = importDeck(await file.text(), arena.catalog, draft.faction);
        if (this.draft !== draft) return;
        this.draft = imported.deck;
        this.profileId = null;
        this.nameInput.value = imported.name;
        this.notice.textContent = '导入已载入草稿；检查后保存为新卡组。同名时请先改名。';
        this.draw();
      } catch (error) {
        if (this.draft === draft) this.notice.textContent = error.message;
      } finally {
        fileInput.value = '';
      }
    };
    document.getElementById('edit-deck').onclick = () => this.open();
    document.getElementById('deck-cancel').onclick = () => this.close();
    this.dialog.addEventListener('cancel', (event) => {
      event.preventDefault();
      this.close();
    });
    document.getElementById('deck-reset').onclick = () => {
      this.draft = arena.decks.get(this.draft.faction, DEFAULT_PRESET);
      this.draw();
    };
    this.saveButton.onclick = () => {
      try {
        arena.decks.save(this.draft, {
          id: this.profileId,
          name: this.nameInput.value,
        });
        this.updateSummary();
        this.close();
      } catch (error) {
        document.getElementById('deck-validation').textContent =
          `保存失败：${error.message}。已保存的卡组保持不变。`;
      }
    };
  }
  updateSummary() {
    const faction = this.select.value;
    const stats = validateStoredDeck(this.arena.decks.profile(faction).deck, this.arena.catalog);
    const warning = this.arena.decks.warnings.includes(faction)
      ? ' · 已保存数据不可用，已恢复预设'
      : '';
    document.getElementById('deck-summary').textContent =
      `${this.arena.decks.saved.has(faction) ? '自定义' : isRandomSelection(this.arena.decks.activeId(faction)) ? '随机' : '预设'}卡组 · ${stats.total} 张 · ${stats.units} 单位 / ${stats.specials} 特殊 · ${this.arena.decks.profile(faction).name}${warning}`;
    if (heroCount(this.arena.decks.profile(faction).deck, this.arena.catalog) > MAX_HEROES)
      document.getElementById('deck-summary').textContent +=
        ` · 英雄超限，已保留原卡组，请编辑为最多 ${MAX_HEROES} 张后开战`;
    for (const side of ['player', 'opponent']) {
      const picker = document.getElementById(`${side}-deck`);
      const chosenFaction = document.getElementById(`${side}-faction`).value;
      const selected = side === 'player' ? this.arena.decks.activeId(chosenFaction) : picker.value;
      picker.replaceChildren(
        ...this.arena.decks.list(chosenFaction, side).map((profile) => {
          const option = document.createElement('option');
          option.value = profile.id;
          option.dataset.kind = profile.kind;
          option.textContent = `${profile.kind === 'custom' ? '自定义 · ' : ''}${profile.name} · ${validateStoredDeck(profile.deck, this.arena.catalog).total} 张${heroCount(profile.deck, this.arena.catalog) > MAX_HEROES ? ' · 待调整' : ''}`;
          return option;
        }),
      );
      picker.value = [...picker.options].some((option) => option.value === selected)
        ? selected
        : side === 'opponent'
          ? 'random-preset'
          : DEFAULT_PRESET;
      const profile = this.arena.decks.profile(chosenFaction, picker.value, side);
      const map = new Map(this.arena.catalog.map((card) => [card.id, card]));
      const heroes = profile.deck.cards.reduce(
        (sum, entry) => sum + (map.get(entry.id).type === 'hero' ? entry.count : 0),
        0,
      );
      const help = document.getElementById(`${side}-deck-help`);
      help.style.setProperty('--card-color', models[chosenFaction].color);
      help.setAttribute('aria-label', `${models[chosenFaction].short} 卡组说明`);
      const lines = [
        `${profile.name} · ${profile.difficulty || '自定义'} · 英雄 ${heroes}/${MAX_HEROES}`,
        heroes > MAX_HEROES ? '英雄超限：原卡组已保留，编辑为最多四张英雄后才能开战。' : '',
        profile.description || '你的自定义卡组，可继续编辑与导出。',
        profile.coreIds?.length
          ? `核心：${profile.coreIds
              .slice(0, 4)
              .map((id) => map.get(id).name)
              .join('、')}`
          : '',
        isRandomSelection(profile.id)
          ? '随机结果已固定；点击重新随机可换一套，整场对局沿用当前卡组。'
          : '',
      ];
      setTooltip(help, lines.filter(Boolean).join('\n'));
      document.getElementById(`${side}-deck-reroll`).hidden = !isRandomSelection(picker.value);
    }
    document.dispatchEvent(new CustomEvent('arena-deck-change'));
  }
  open() {
    if (!document.getElementById('lobby').hidden && !this.arena.view.modalOpen) {
      const profile = this.arena.decks.profile(this.select.value);
      this.draft = this.arena.decks.copy(profile.deck);
      this.profileId = isBuiltInSelection(profile.id) ? null : profile.id;
      this.nameInput.value =
        profile.id === 'preset'
          ? '我的卡组'
          : isBuiltInSelection(profile.id)
            ? `${profile.name} · 我的版本`
            : profile.name;
      this.notice.textContent = '修改、重命名或导入后点击保存生效；新建会复制当前草稿。';
      document.getElementById('deck-query').value = '';
      document.getElementById('deck-kind').value = 'all';
      document.getElementById('deck-selected').checked = false;
      document.getElementById('deck-editor-title').textContent =
        `${models[this.draft.faction].short} · 配置卡组`;
      this.draw();
      this.dialog.showModal();
      document.getElementById('deck-cancel').focus();
    }
  }
  close() {
    this.dialog.close();
    this.draft = null;
    document.getElementById('edit-deck').focus();
  }
  count(id) {
    return this.draft.cards.find((entry) => entry.id === id)?.count || 0;
  }
  change(card, delta) {
    const count = this.count(card.id) + delta;
    if (count < 0 || count > card.maxCopies) return;
    this.draft.cards = this.draft.cards.filter((entry) => entry.id !== card.id);
    if (count) this.draft.cards.push({ id: card.id, count });
    this.refresh();
  }
  draw() {
    this.drawLeader();
    this.controls = [];
    document.getElementById('deck-grid').replaceChildren(
      ...sortCards(this.arena.catalog)
        .filter(
          (card) =>
            card.type !== 'leader' && [this.draft.faction, 'neutral'].includes(card.faction),
        )
        .map((card) => {
          const item = document.createElement('article');
          item.className = 'deck-choice';
          item.dataset.deckCard = card.id;
          const controls = document.createElement('div');
          controls.className = 'deck-count';
          const minus = document.createElement('button');
          const plus = document.createElement('button');
          const label = document.createElement('span');
          for (const [button, delta, text] of [
            [minus, -1, '−'],
            [plus, 1, '+'],
          ]) {
            button.type = 'button';
            button.textContent = text;
            button.setAttribute('aria-label', `${delta > 0 ? '增加' : '减少'} ${card.name}`);
            button.onclick = () => this.change(card, delta);
          }
          label.setAttribute('aria-label', `${card.name} 数量`);
          controls.append(minus, label, plus);
          const details = document.createElement('details'),
            summary = document.createElement('summary'),
            description = document.createElement('p');
          summary.textContent = '技能说明';
          description.textContent = cardDescription(card);
          details.append(summary, description);
          item.append(cardElement(card), controls, details);
          this.controls.push({ card, item, minus, plus, label });
          return item;
        }),
    );
    this.refresh();
  }
  drawLeader() {
    const leaders = leaderDefinitions(this.arena.catalog).filter(
      (card) => card.faction === this.draft.faction,
    );
    this.leaderPicker.replaceChildren(...leaders.map((card) => new Option(card.name, card.id)));
    this.leaderPicker.value = this.draft.leader;
    const leader = leaders.find((card) => card.id === this.draft.leader);
    const explanation = leader
      ? `${leader.name}\n${cardDescription(leader)}\n本阵营三种领袖方案，每套卡组选一种，整场不能切换。主动技能占用一次行动，换小局不会重置次数。`
      : '无效领袖';
    setTooltip(this.leaderHelp, explanation);
    setTooltip(this.leaderPicker, explanation);
  }
  refresh() {
    const heroes = heroCount(this.draft, this.arena.catalog);
    let units = 0,
      specials = 0,
      power = 0;
    for (const { card, item, minus, plus, label } of this.controls) {
      const count = this.count(card.id);
      label.textContent = `${count} / ${card.maxCopies}`;
      minus.disabled = count === 0;
      plus.disabled = count === card.maxCopies || (card.type === 'hero' && heroes >= MAX_HEROES);
      item.classList.toggle('excluded', count === 0);
      if (['unit', 'hero'].includes(card.type)) {
        units += count;
        power += count * card.power;
      } else specials += count;
    }
    document.getElementById('deck-totals').textContent =
      `${units + specials} 张卡牌 · ${units} 单位 · ${specials} 特殊 · 基础战力 ${power}`;
    let message = '卡组合法，可以保存。';
    const exportButton = document.getElementById('deck-export');
    exportButton.disabled = true;
    try {
      validateStoredDeck(this.draft, this.arena.catalog);
      const name = this.nameInput.value.trim();
      exportButton.disabled = !name || name.length > 40;
    } catch {}
    try {
      validateDeck(this.draft, this.arena.catalog);
      const name = this.nameInput.value.trim();
      if (!name || name.length > 40) throw Error('卡组名称需要 1–40 个字符');
      exportButton.disabled = false;
      if (
        (!this.profileId || this.profileId === 'preset') &&
        this.arena.decks.record(this.draft.faction).profiles.length >= MAX_PROFILES
      )
        throw Error(`每个阵营最多保存 ${MAX_PROFILES} 套卡组`);
      if (
        this.arena.decks
          .record(this.draft.faction)
          .profiles.some((profile) => profile.id !== this.profileId && profile.name === name)
      )
        throw Error('同名卡组已存在，请修改名称');
      this.saveButton.disabled = false;
    } catch (error) {
      message = error.message;
      this.saveButton.disabled = true;
    }
    document.getElementById('deck-validation').textContent = message;
    document.getElementById('deck-delete').disabled =
      !this.profileId || this.profileId === 'preset';
    const totals = { hero: 0, close: 0, ranged: 0, siege: 0, agile: 0 };
    for (const { card } of this.controls) {
      if (card.type === 'hero') totals.hero += this.count(card.id);
      if (card.row in totals) totals[card.row] += this.count(card.id);
    }
    document.getElementById('deck-composition').textContent =
      `英雄 ${totals.hero}/${MAX_HEROES} · 普通单位 ${units - totals.hero} · 推理 ${totals.close} / 感知 ${totals.ranged} / 算力 ${totals.siege} / 双排 ${totals.agile}`;
    this.filter();
  }
  filter() {
    let visible = 0;
    for (const { card, item } of this.controls) {
      item.hidden =
        !matchesCard(
          card,
          document.getElementById('deck-query').value,
          document.getElementById('deck-kind').value,
        ) ||
        (document.getElementById('deck-selected').checked && !this.count(card.id));
      if (!item.hidden) visible++;
    }
    document.getElementById('deck-filter-count').textContent =
      `显示 ${visible} / ${this.controls.length} 种可用卡牌 · 旗舰优先`;
  }
}
