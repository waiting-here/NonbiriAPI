/* eslint-disable */
import { models } from '../ai/models.js';
import { ArenaAI } from '../ai/ai-engine.js';
import { matchMode } from './modes.js';
import { DecisionClock, timingPolicy } from './decision-clock.js';
import { installDecisionTimer } from '../ui/decision-timer.js';
import { installDeckOperations } from './deck.js';
import { registerAbilities } from '../cards/abilities.js';
import { validateCatalog, validateDeck } from './rules.js';
import { executeAction, legalActions, decisionSnapshot } from './battle.js';
import { ArenaView } from '../ui/battlefield.js';
import { tone } from '../ui/animation.js';
import { factionRules, registerFaction, roundDifference } from './factions.js';
import { DeckStore } from '../cards/decks.js';
import { runtimeForms, summonOnDeparture } from '../cards/forms.js';
import { leaderDefinitions } from '../cards/leaders.js';
import { loadPreferences, applyMotion } from '../ui/settings.js';

export class ArenaEngine {
  constructor(catalog, storage = localStorage) {
    this.catalog = validateCatalog(catalog);
    this.decks = new DeckStore(catalog, storage);
    this.view = new ArenaView(this);
    this.busy = false;
    this.mode = 'play';
    this.matchMode = 'local';
    this.matchId = 0;
    this.decisionCount = 0;
    this.matchAbort = new AbortController();
    this.timing = { ...timingPolicy };
    this.clock = new DecisionClock({ onChange: installDecisionTimer() });
    document.addEventListener('visibilitychange', () => this.clock.tick());
    this.preferences = loadPreferences(localStorage);
    this.speed = this.preferences.speed;
    applyMotion(this.preferences.motion);
    this.roundSummaries = [];
    this.finished = false;
    const definitions = [
      ...catalog,
      ...runtimeForms(catalog),
      ...leaderDefinitions(catalog).filter((card) => card.choiceOf),
    ];
    this.index = new Map(definitions.map((card, index) => [card.id, index]));
    this.byName = new Map(definitions.map((card) => [card.name, card]));
    registerAbilities(this);
    for (const card of definitions)
      for (const id of card.abilities) if (!ability_dict[id]) throw Error('未注册技能：' + id);
    card_dict = definitions.map((card) => ({
      id: card.id,
      name: card.name,
      deck: card.type === 'weather' ? 'weather' : card.type === 'skill' ? 'special' : card.faction,
      row: card.row,
      strength: String(card.power),
      ability: [...(card.type === 'hero' ? ['hero'] : []), ...card.abilities].join(' '),
      filename: card.id,
      count: String(card.maxCopies),
    }));
    factions = Object.fromEntries(
      Object.entries(models).map(([id, model]) => [
        id,
        {
          name: model.name,
          description: `${model.description} 阵营被动：${factionRules[id].description}`,
          factionAbility: (player) => registerFaction(this, player),
        },
      ]),
    );
    this.installBridge();
    dm = {
      constructOpponentDeck: () =>
        this.deck(
          document.getElementById('opponent-faction').value,
          false,
          document.getElementById('opponent-deck').value,
        ),
    };
  }
  rows(player) {
    return ['close', 'ranged', 'siege'].map((row) => board.getRow({ abilities: [] }, row, player));
  }
  label(player) {
    return `${models[player.deck.faction].short}（${player === player_me ? '己方' : '对手'}）`;
  }
  captureRound() {
    this.view.suppressFeedback = true;
    const record = {
      round: game.roundCount,
      total: { me: player_me.total, op: player_op.total },
      before: { me: player_me.health, op: player_op.health },
      rows: ['close', 'ranged', 'siege'].map((row, index) => ({
        row,
        me: this.rows(player_me)[index].total,
        op: this.rows(player_op)[index].total,
      })),
    };
    this.roundSummaries.push(record);
  }
  delay(ms) {
    return new Promise((resolve) => setTimeout(resolve, Math.max(1, ms * this.speed)));
  }
  canAct() {
    if (this.pvp?.active) return this.pvp.canAct();
    return (
      !this.busy &&
      !this.finished &&
      !this.view.modalOpen &&
      this.mode === 'play' &&
      game.state === GameState.PLAYING &&
      game.currPlayer === player_me &&
      (this.clock.phase !== 'turn' || this.clock.remainingMs > 0) &&
      !player_me?.passed
    );
  }
  deck(faction, custom = false, profileId = 'preset') {
    const profile = this.decks.profile(
      faction,
      custom ? undefined : profileId,
      custom ? 'player' : 'opponent',
    );
    const data = profile.deck;
    validateDeck(data, this.catalog);
    return {
      faction,
      leader: card_dict[this.index.get(data.leader)],
      cards: data.cards.map((card) => ({
        index: this.index.get(card.id),
        count: card.count,
      })),
    };
  }
  installBridge() {
    const arena = this;
    installDeckOperations();
    game.arenaRoundDifference = (me, op) =>
      roundDifference(me.deck.faction, op.deck.faction, me.total, op.total);
    const endPlayerTurn = Player.prototype.endTurn;
    Player.prototype.endTurn = function (...args) {
      // Restore the human controller before the next turn can begin.
      if (this.arenaOriginalController) {
        this.controller = this.arenaOriginalController;
        delete this.arenaOriginalController;
      }
      return endPlayerTurn.apply(this, args);
    };
    // This bridge is installed only by index.html; classic.html keeps upstream behavior.
    Card.prototype.createCardElem = function () {
      this.arenaData = arena.byName.get(this.name);
      const element = document.createElement('div');
      element.className = 'card';
      for (let index = 0; index < 4; index++) {
        const child = document.createElement('div');
        if (index === 0) child.append(document.createElement('div'));
        element.append(child);
      }
      return element;
    };
    Card.prototype.id = function () {
      return this.arenaData.id;
    };
    Card.prototype.isSpecial = function () {
      return this.arenaData.type === 'skill' && this.abilities.includes('horn');
    };
    Card.prototype.animate = async function (id) {
      tone(id);
      arena.view.render();
      arena.view.effect(this, id);
      await arena.delay(160);
    };
    const score = Row.prototype.calcCardScore;
    Row.prototype.calcCardScore = function (card) {
      if (card.abilities.includes('decoy')) return 0;
      const copy = Object.create(card);
      copy.basePower = card.basePower + (card.hero ? card.arenaGrowth || 0 : card.arenaBonus || 0);
      return score.call(this, copy);
    };
    const updateScore = Row.prototype.updateScore;
    Row.prototype.updateScore = function () {
      const result = updateScore.call(this);
      arena.view.render();
      return result;
    };
    const reset = Card.prototype.resetPower;
    Card.prototype.resetPower = function () {
      this.arenaGrowth = 0;
      this.arenaBonus = 0;
      return reset.call(this);
    };
    const move = board.moveTo.bind(board);
    board.moveTo = async function (card, dest, source) {
      const destination = typeof dest === 'string' ? board.getRow(card, dest) : dest;
      const departure =
        source instanceof Row && source.cards.includes(card) && !(destination instanceof Row);
      if (card.arenaData.ephemeral && destination === card.holder.grave) {
        if (source?.cards.includes(card) || source?.special === card) source.removeCard(card);
        arena.view.log(`衍生单位离场 · ${card.name} 消失。`);
        arena.view.render();
        return;
      }
      if (source === card.holder?.hand && card.isUnit() && card.holder.arenaBoost) {
        arena.view.log(`${card.name} 消耗待强化，基础战力 +${card.holder.arenaBoost}。`);
        card.arenaBonus = (card.arenaBonus || 0) + card.holder.arenaBoost;
        card.holder.arenaBoost = 0;
      }
      const result = await move(card, dest, source);
      if (departure) await summonOnDeparture(arena, card, source);
      arena.view.render();
      if (dest instanceof Row && !arena.view.suppressFeedback) arena.view.effect(card, 'play');
      return result;
    };
    const draw = Deck.prototype.draw;
    Deck.prototype.draw = async function (hand) {
      const count = hand.cards.length,
        card = this.cards[0];
      const result = await draw.call(this, hand);
      if (hand.cards.length > count && game.roundCount > 0) {
        const player = hand === player_me.hand ? player_me : player_op;
        const visible = player === player_me || arena.mode === 'watch';
        arena.view.log(
          `${arena.label(player)} 抽取一张牌${visible ? ` · ${card.name}` : ''}，手牌 ${hand.cards.length}。`,
        );
        arena.view.render();
      }
      return result;
    };
    const swap = Deck.prototype.swap;
    Deck.prototype.swap = function (container, card) {
      const result = swap.call(this, container, card);
      if (card) {
        const player = container === player_me.hand ? player_me : player_op;
        arena.view.log(`${arena.label(player)} 重抽一张手牌，手牌数量不变。`);
        arena.view.render();
      }
      return result;
    };
    const clearWeather = weather.clearWeather.bind(weather);
    weather.clearWeather = async function () {
      const hadWeather = this.cards.length > 0;
      const result = await clearWeather();
      if (hadWeather && !arena.view.suppressFeedback)
        arena.view.log('全部天气解除 · 普通单位恢复基础战力。');
      arena.view.render();
      return result;
    };
    // Weather identities no longer depend on localized display names.
    weather.addCard = async function (card) {
      if (card.abilities.includes('clear')) {
        await board.toGrave(card);
        await this.clearWeather();
      } else {
        const duplicate = this.cards.some((target) => target.abilities[0] === card.abilities[0]);
        CardContainer.prototype.addCard.call(this, card);
        this.changeWeather(
          card,
          (key) => ++this.types[key].count === 1,
          (row, type) => row.addOverlay(type.name),
        );
        if (duplicate) await board.toGrave(card, this);
        if (!arena.view.suppressFeedback)
          arena.view.log(
            duplicate
              ? '同类天气已生效，重复天气不叠加。'
              : `${ability_dict[card.abilities[0]].name} 生效 · 双方对应战线普通单位基础战力压低至最多 1，英雄免疫。`,
          );
      }
      arena.view.render();
    };
    translateTo = async () => {};
    sleep = (ms) => arena.delay(ms);
    fadeIn = async (element) => element?.classList.remove('hide');
    fadeOut = async (element) => element?.classList.add('hide');
    iconURL = () => '';
    AudioManager.playSFX = async (key) => tone(key);
    ui.enablePlayer = (enable) => {
      if (game.state === GameState.PLAYING) arena.busy = !enable || arena.mode === 'watch';
      if (
        enable &&
        !arena.finished &&
        game.state === GameState.PLAYING &&
        arena.mode === 'play' &&
        game.roundCount > 0 &&
        game.currPlayer === player_me &&
        !(player_me.controller instanceof ControllerAI)
      ) {
        const matchId = arena.matchId;
        arena.clock.arm('turn', arena.timing.turnMs, () => arena.expireHumanTurn(matchId));
      } else if (arena.clock.phase === 'turn') arena.clock.stop();
      arena.view.render();
    };
    ui.showPreviewVisuals = (card) => {
      arena.view.log(
        `${models[card.holder.deck.faction].short} ${card.row === 'leader' ? '激活' : '打出'} ${card.name}`,
      );
      arena.view.preview(card);
      tone('play');
    };
    ui.hidePreview = () => {
      arena.view.selection = null;
      arena.view.render();
    };
    ui.notification = async (key) => {
      if (key === 'round-start') {
        arena.view.suppressFeedback = false;
        arena.view.scores = new WeakMap();
        document
          .querySelectorAll('[data-change]')
          .forEach((element) => element.removeAttribute('data-change'));
        arena.view.log(`第 ${game.roundCount} 小局开始。`);
        if (game.roundCount === 1)
          for (const player of [player_me, player_op])
            arena.view.log(
              `${arena.label(player)} · 阵营被动：${player.arenaPassive?.description || factionRules[player.deck.faction].description}`,
            );
      }
      if (['me-pass', 'op-pass'].includes(key)) {
        const player = key === 'me-pass' ? player_me : player_op;
        arena.view.log(
          `${arena.label(player)} 已放弃本小局 · 战力 ${player.total}，剩余手牌 ${player.hand.cards.length}。`,
        );
      }
      if (['win-round', 'lose-round', 'draw-round'].includes(key)) {
        const record = arena.roundSummaries.at(-1);
        if (record) {
          record.after = { me: player_me.health, op: player_op.health };
          arena.view.showRound(record);
          arena.view.log(
            `第 ${record.round} 小局结算 · ${record.total.me} : ${record.total.op} · ${key === 'draw-round' ? '平局，双方扣一颗生命' : key === 'win-round' ? '己方获胜' : '对手获胜'}。`,
          );
        }
      }
      await arena.delay(80);
    };
    ui.queueCarousel = async (container, count, action, predicate, sort, quit, title) => {
      if (game.currPlayer?.controller instanceof ControllerAI) {
        for (let index = 0; index < count; index++) {
          const options = container.cards
            .map((card, index) => ({ card, index }))
            .filter(({ card }) => !predicate || predicate(card));
          if (!options.length) break;
          await action(container, options[0].index);
        }
        return;
      }
      return arena.view.chooseCards(
        container,
        count,
        action,
        predicate,
        quit,
        title || '恢复 · 选择一张卡牌',
      );
    };
    ui.waitForRowSelection = (card) => arena.view.chooseRow(card);
    game.initialRedraw = async () => {
      if (arena.mode === 'watch') {
        for (let index = 0; index < 2; index++) {
          player_me.controller.redraw();
          player_op.controller.redraw();
        }
      } else {
        for (let index = 0; index < 2; index++) player_op.controller.redraw();
        await arena.view.chooseCards(
          player_me.hand,
          2,
          (container, index) => player_me.deck.swap(container, container.cards[index]),
          () => true,
          true,
          '开局调度 · 最多重抽两张',
        );
      }
      ui.enablePlayer(false);
    };
    for (const method of ['startGame', 'startRound', 'startTurn', 'endTurn', 'endRound']) {
      const original = game[method].bind(game);
      game[method] = async function (...args) {
        if (['endTurn', 'endRound'].includes(method)) arena.clock.stop();
        try {
          const result = await original(...args);
          arena.view.render();
          return result;
        } catch (error) {
          arena.fail(error);
        }
      };
    }
    const endGame = game.endGame.bind(game);
    game.endGame = async () => {
      arena.finished = true;
      arena.clock.stop();
      await endGame();
      arena.matchAbort.abort();
      arena.llmThinking = false;
      arena.view.render();
      arena.showResult();
    };
    game.rematchGame = () => arena.start(arena.mode);
    game.newOpponentGame = () => arena.start(arena.mode);
    game.returnToCustomization = () => arena.lobby();
    EventManager.roundEnded = new GameEvent('arena-round-ended', [
      'round',
      'points-me',
      'points-op',
    ]);
  }
  async start(mode = 'play', opponentMode = this.matchMode) {
    if (this.pvp?.active) return;
    if (game.state === GameState.PLAYING) return;
    matchMode(opponentMode);
    if (opponentMode === 'pvp') throw Error('请创建或加入房间，双方准备后开始对战');
    if (mode === 'watch' && opponentMode !== 'local') throw Error('观战模式仅支持本地 AI');
    const matchDecks = {
      me: this.decks.profile(document.getElementById('player-faction').value),
      op: this.decks.profile(
        document.getElementById('opponent-faction').value,
        document.getElementById('opponent-deck').value,
        'opponent',
      ),
    };
    for (const profile of Object.values(matchDecks)) validateDeck(profile.deck, this.catalog);
    this.matchMode = opponentMode;
    this.clock.stop();
    this.matchAbort.abort();
    this.matchAbort = new AbortController();
    this.matchId++;
    this.decisionCount = 0;
    this.llmThinking = false;
    this.mode = mode;
    this.finished = false;
    this.busy = true;
    this.view.selection = null;
    this.view.logItems = [];
    this.roundSummaries = [];
    this.view.reset();
    document.querySelector('.shell').inert = false;
    document.getElementById('result-overlay').hidden = true;
    document.getElementById('lobby').hidden = true;
    document.getElementById('battle').hidden = false;
    game.reset();
    this.matchDecks = matchDecks;
    for (const side of ['me', 'op'])
      document.getElementById(`leader-${side}`).children[0].replaceChildren();
    player_me = new Player(
      0,
      models[document.getElementById('player-faction').value].short,
      this.deck(document.getElementById('player-faction').value, true),
    );
    player_op = new Player(
      1,
      models[document.getElementById('opponent-faction').value].short,
      this.deck(
        document.getElementById('opponent-faction').value,
        false,
        document.getElementById('opponent-deck').value,
      ),
    );
    player_op.controller = new ArenaAI(player_op, this);
    if (mode === 'watch') player_me.controller = new ArenaAI(player_me, this);
    game.roundEnd.push(() => {
      this.captureRound();
      for (const player of [player_me, player_op]) {
        player.arenaBoost = 0;
        player.arenaShield = 0;
      }
      return false;
    });
    this.view.render();
    await game.startGame();
  }
  async act(card, row) {
    if (this.pvp?.active) return this.pvp.play(card.instanceId, row?.type);
    if (!this.canAct()) return;
    const action = legalActions(player_me, this).find(
      (action) => action.card === card && (!row || action.row === row),
    );
    if (!action) return;
    if (this.clock.phase === 'turn' && !this.clock.claim()) return;
    this.busy = true;
    this.view.selection = null;
    this.view.render();
    try {
      await executeAction(player_me, action, this);
    } catch (error) {
      this.fail(error);
    }
  }
  async command(type) {
    if (this.pvp?.active) return this.pvp.command(type);
    if (!this.canAct()) return;
    if (this.clock.phase === 'turn' && !this.clock.claim()) return;
    this.busy = true;
    this.view.selection = null;
    this.view.render();
    if (type === 'concede') {
      player_me.health = 0;
      await game.endGame();
      return;
    }
    try {
      await executeAction(player_me, { type }, this);
    } catch (error) {
      this.fail(error);
    }
  }
  showResult() {
    const draw = player_me.health === 0 && player_op.health === 0,
      win = player_op.health === 0 && !draw;
    document.getElementById('result-title').textContent =
      this.mode === 'watch'
        ? draw
          ? '双方平局'
          : `${models[(win ? player_me : player_op).deck.faction].short} 胜出`
        : draw
          ? '势均力敌'
          : win
            ? '推演成功'
            : '算力不敌';
    document.getElementById('result-subtitle').textContent = game.roundHistory.length
      ? game.roundHistory
          .map((round, index) => `第 ${index + 1} 局 ${round.score_me} : ${round.score_op}`)
          .join(' · ')
      : '已认输';
    document.getElementById('result-overlay').hidden = false;
    this.view.resultHistory(this.roundSummaries);
    document.querySelector('.shell').inert = true;
    document.getElementById('rematch').focus();
    tone('win');
  }
  lobby() {
    if (this.pvp?.active) return this.pvp.leave();
    if (game.state === GameState.PLAYING) return;
    document.getElementById('result-overlay').hidden = true;
    document.getElementById('battle').hidden = true;
    document.getElementById('lobby').hidden = false;
    document.querySelector('.shell').inert = false;
    document.getElementById('launch-play').focus();
  }
  snapshot(player = player_me) {
    return decisionSnapshot(player, this);
  }
  async expireHumanTurn(matchId) {
    if (
      matchId !== this.matchId ||
      this.finished ||
      this.busy ||
      this.mode !== 'play' ||
      game.state !== GameState.PLAYING ||
      game.currPlayer !== player_me ||
      player_me.passed
    )
      return;
    const player = player_me;
    this.busy = true;
    this.view.expireDialog();
    this.view.selection = null;
    const ai = new ArenaAI(player, this);
    const action = ai.chooseAction(player);
    this.view.log('你的回合已超时 · 本地 AI 代行动一次，下一回合继续由你操作。');
    player.arenaOriginalController = player.controller;
    player.controller = ai;
    this.view.render();
    try {
      await executeAction(player, action, this);
    } catch (error) {
      this.fail(error);
    } finally {
      if (player.arenaOriginalController) {
        player.controller = player.arenaOriginalController;
        delete player.arenaOriginalController;
      }
    }
  }
  fail(error) {
    this.clock.stop();
    console.error(error);
    this.view.log('对局错误：' + error.message);
    this.view.announce('对局发生错误，请刷新后重试。');
    this.busy = true;
  }
}
