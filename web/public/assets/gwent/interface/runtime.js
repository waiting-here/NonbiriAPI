/* eslint-disable */
'use strict';

class Enum {
  constructor(val) {
    this.val = val;
  }
  toString() {
    return this.val;
  }
}

const DURATION_CARD_PLACEMENT = 1000;

const DUR_FADE_STEP = 10;

const CLICK_EVENT_SFX = () => AudioManager.playSFX('ui_card');

const addMouseEnterSFXBySelector = (selector) => {
  [...document.querySelectorAll(selector)].forEach((e) =>
    e.addEventListener('mouseenter', CLICK_EVENT_SFX),
  );
};

class Controller {}

// Makes decisions for the AI opponent player
class ControllerAI {
  constructor(player) {
    this.player = player;
  }

  // Collects data and weighs options before taking a weighted random action
  async startTurn(player) {
    if (
      player.opponent().passed &&
      (player.winning ||
        (player.deck.faction === 'nilfgaard' && player.total === player.opponent().total))
    ) {
      await player.passRound();
      return;
    }
    let data_max = this.getMaximums();
    let data_board = this.getBoardData();
    let weights = player.hand.cards.map((c) => ({
      weight: this.weightCard(c, data_max, data_board),
      action: async () => await this.playCard(c, data_max, data_board),
    }));
    if (player.leaderAvailable)
      weights.push({
        weight: this.weightLeader(player.leader, data_max, data_board),
        action: async () => await player.activateLeader(),
      });
    weights.push({ weight: this.weightPass(), action: async () => await player.passRound() });
    let weightTotal = weights.reduce((a, c) => a + c.weight, 0);
    if (weightTotal === 0) {
      for (let i = 0; i < player.hand.cards.length; ++i) {
        let card = player.hand.cards[i];
        if (
          (card.row === 'weather' && this.weightWeather(card) > -1) ||
          card.abilities.includes('avenger')
        ) {
          await weights[i].action();
          return;
        }
      }
      await player.passRound();
    } else {
      let rand = randomInt(weightTotal);
      for (var i = 0; i < weights.length; ++i) {
        rand -= weights[i].weight;
        if (rand < 0) break;
      }
      await weights[i].action();
    }
  }

  // Collects data about card with the hightest power on the board
  getMaximums() {
    let rmax = board.row.map((r) => ({
      row: r,
      cards: r.cards
        .filter((c) => c.isUnit())
        .reduce(
          (a, c) =>
            !a.length || a[0].power < c.power ? [c] : a[0].power === c.power ? a.concat([c]) : a,
          [],
        ),
    }));

    let max = rmax
      .filter((r, i) => r.cards.length && i < 3)
      .reduce((a, r) => Math.max(a, r.cards[0].power), 0);
    let max_me = rmax
      .filter((r, i) => i < 3 && r.cards.length && r.cards[0].power === max)
      .reduce((a, r) => a.concat(r.cards.map((c) => ({ row: r, card: c }))), []);

    max = rmax
      .filter((r, i) => r.cards.length && i > 2)
      .reduce((a, r) => Math.max(a, r.cards[0].power), 0);
    let max_op = rmax
      .filter((r, i) => i > 2 && r.cards.length && r.cards[0].power === max)
      .reduce((a, r) => a.concat(r.cards.map((c) => ({ row: r, card: c }))), []);

    return { rmax: rmax, me: max_me, op: max_op };
  }

  // Collects data about the types of cards on the board and in each player's graves
  getBoardData() {
    let data = this.countCards(new CardContainer());
    Object.keys([0, 1, 2])
      .map((i) => board.row[i])
      .forEach((r) => this.countCards(r, data));
    data.grave_me = this.countCards(this.player.grave);
    data.grave_op = this.countCards(this.player.opponent().grave);
    return data;
  }

  // Catalogs the kinds of cards in a given CardContainer
  countCards(container, data) {
    data = data ? data : { spy: [], medic: [], bond: {}, scorch: [] };
    container.cards
      .filter((c) => c.isUnit())
      .forEach((c) => {
        for (let x of c.abilities) {
          switch (x) {
            case 'spy':
            case 'medic':
              data[x].push(c);
              break;
            case 'scorch_r':
            case 'scorch_c':
            case 'scorch_s':
              data['scorch'].push(c);
              break;
            case 'bond':
              if (!data.bond[c.name]) data.bond[c.name] = 0;
              data.bond[c.name]++;
          }
        }
      });
    return data;
  }

  // Swaps a card from the hand with the deck if beneficial
  redraw() {
    const card = this.discardOrder({ holder: this.player })[0];
    if (card && card.power < 15) {
      this.player.deck.swap(this.player.hand, card);
    }
  }

  // Orders discardable cards from most to least discardable
  discardOrder(card) {
    let cards = [];
    let groups = {};
    let musters = card.holder.hand.cards.filter((c) => c.abilities.includes('muster'));
    while (musters.length > 0) {
      let curr = musters.pop();
      let i = curr.name.indexOf('-');
      let name = i === -1 ? curr.name : curr.name.substring(0, i).trim();
      if (!groups[name]) groups[name] = [];
      let group = groups[name];
      group.push(curr);
      for (let j = musters.length - 1; j >= 0; j--)
        if (musters[j].name.startsWith(name)) group.push(musters.splice(j, 1)[0]);
    }

    for (let group of Object.values(groups)) {
      group.sort(Card.compare);
      group.pop();
      cards.push(...group);
    }

    let weathers = card.holder.hand.cards.filter((c) => c.row === 'weather');
    if (weathers.length > 1) {
      weathers.splice(randomInt(weathers.length), 1);
      cards.push(...weathers);
    }

    let normal = card.holder.hand.cards.filter((c) => c.abilities.length === 0);
    normal.sort(Card.compare);
    cards.push(...normal);
    return cards;
  }

  // Tells the Player that this object controls to play a card
  async playCard(c, max, data) {
    if (c.name === "Commander's Horn") await this.horn(c);
    else if (c.name === 'Mardroeme') await this.mardroeme(c);
    else if (c.name === 'Decoy') await this.decoy(c, max, data);
    else if (c.name === 'Scorch') await this.scorch(c, max, data);
    else await this.player.playCard(c);
  }

  // Plays a Commander's Horn to the most beneficial row. Assumes at least one viable row.
  async horn(card) {
    let rows = [0, 1, 2].map((i) => board.row[i]).filter((r) => r.special === null);
    let max_row;
    let max = 0;
    for (let i = 0; i < rows.length; ++i) {
      let r = rows[i];
      let dif = [0, 0];
      this.calcRowPower(r, dif, true);
      r.effects.horn++;
      this.calcRowPower(r, dif, false);
      r.effects.horn--;
      let score = dif[1] - dif[0];
      if (max < score) {
        max = score;
        max_row = r;
      }
    }
    await this.player.playCardToRow(card, max_row);
  }

  // Plays a Mardroeme to the most beneficial row. Assumes at least one viable row.
  async mardroeme(card) {
    // TODO skellige
    let row,
      max = 0;
    for (let i = 1; i < 3; i++) {
      let curr = this.weightMardroemeRow(card, board.row[i]);
      if (curr > max) {
        max = curr;
        row = board.row[i];
      }
    }
    await this.player.playCardToRow(card, row);
  }

  // Selects a card to remove from a Grave. Assumes at least one valid card.
  medic(card, grave) {
    let data = this.countCards(grave);
    let targ;
    if (data.spy.length) {
      let min = data.spy.reduce((a, c) => Math.min(a, c.power), Number.MAX_VALUE);
      targ = data.spy.filter((c) => c.power === min)[0];
    } else if (data.medic.length) {
      let max = data.medic.reduce((a, c) => Math.max(a, c.power), Number.MIN_VALUE);
      targ = data.medic.filter((c) => c.power === max)[0];
    } else if (data.scorch.length) {
      targ = data.scorch[randomInt(data.scorch.length)];
    } else {
      let units = grave.findCards((c) => c.isUnit());
      targ = units.reduce((a, c) => (a.power < c.power ? c : a), units[0]);
    }
    return targ;
  }

  // Selects a card to return to the Hand and replaces it with a Decoy. Assumes at least one valid card.
  async decoy(card, max, data) {
    let targ, row;
    if (data.spy.length) {
      let min = data.spy.reduce((a, c) => Math.min(a, c.power), Number.MAX_VALUE);
      targ = data.spy.filter((c) => c.power === min)[0];
    } else if (data.medic.length) {
      targ = data.medic[randomInt(data.medic.length)];
    } else if (data.scorch.length) {
      targ = data.scorch[randomInt(data.scorch.length)];
    } else {
      let pairs = max.rmax
        .filter((r, i) => i < 3 && r.cards.length)
        .reduce((a, r) => r.cards.map((c) => ({ r: r.row, c: c })).concat(a), []);
      let pair = pairs[randomInt(pairs.length)];
      targ = pair.c;
      row = pair.r;
    }

    for (let i = 0; !row; ++i) {
      if (board.row[i].cards.indexOf(targ) !== -1) {
        row = board.row[i];
        break;
      }
    }

    setTimeout(() => board.toHand(targ, row), 1000);
    await this.player.playCardToRow(card, row);
  }

  // Tells the controlled Player to play the Scorch card
  async scorch(card, max, data) {
    await this.player.playScorch(card);
  }

  // Gets the row that would be best for the passed agile card
  determineAgileRow(card) {
    const close = board.getRow(card, 'close', card.holder);
    const ranged = board.getRow(card, 'ranged', card.holder);
    const vClose = close.getVirtualCopy();
    const vRanged = ranged.getVirtualCopy();
    vClose.cards.push(card);
    vClose.updateState(card, true);
    vRanged.cards.push(card);
    vRanged.updateState(card, true);
    const dif = vClose.calcScore() - close.calcScore() - (vRanged.calcScore() - ranged.calcScore());
    return dif > 0 ? close : dif < 0 ? ranged : Math.random() > 0.5 ? close : ranged;
  }

  // Assigns a weight for how likely the conroller is to Pass the round
  weightPass() {
    if (this.player.health === 1) return 0;
    let dif = this.player.opponent().total - this.player.total;
    if (dif > 30) return 100;
    if (dif < -30 && this.player.opponent().handsize - this.player.handsize > 2) return 100;
    return Math.floor(Math.abs(dif));
  }

  // Assigns a weight for how likely the controller is to activate its leader ability
  weightLeader(card, max, data) {
    let w = ability_dict[card.abilities[0]].weight;
    if (ability_dict[card.abilities[0]].weight) {
      let score = w(card, this, max, data);
      return score;
    }
    return 10 + (game.roundCount - 1) * 15;
  }

  // Assigns a weight for how likely the controller will use a scorch-row card
  weightScorchRow(card, max, row_name) {
    let index = 3 + (row_name === 'close' ? 0 : row_name === 'ranged' ? 1 : 2);
    if (board.row[index].total < 10) return 0;
    let score = max.rmax[index].cards.reduce((a, c) => a + c.power, 0);
    return score;
  }

  // Calculates a weight for how likely the conroller will use horn on this row
  weightHornRow(card, row) {
    return row.special !== null ? 0 : this.weightRowChange(card, row);
  }

  // Calculates weight for playing a card on a given row, min 0
  weightRowChange(card, row) {
    return Math.max(0, this.weightRowChangeTrue(card, row));
  }

  // Calculates weight for playing a card on the given row
  weightRowChangeTrue(card, row) {
    let dif = [0, 0];
    this.calcRowPower(row, dif, true);
    row.updateState(card, true);
    this.calcRowPower(row, dif, false);
    if (!card.isSpecial()) dif[0] -= row.calcCardScore(card);
    row.updateState(card, false);
    return dif[1] - dif[0];
  }

  // Calculates the weight for playing a weather card
  weightWeather(card) {
    let rows;
    if (card.name === 'Clear Weather')
      rows = Object.values(weather.types)
        .filter((t) => t.count > 0)
        .flatMap((t) => t.rows);
    else
      rows = Object.values(weather.types)
        .filter((t) => t.count === 0 && t.name === card.abilities[0])
        .flatMap((t) => t.rows);
    if (!rows.length) return 1;
    let dif = [0, 0];
    rows.forEach((r) => {
      let state = r.effects.weather;
      this.calcRowPower(r, dif, true);
      r.effects.weather = !state;
      this.calcRowPower(r, dif, false);
      r.effects.weather = state;
    });
    return dif[1] - dif[0];
  }

  // Calculates the weight for playing a mardroeme card
  weightMardroemeRow(card, row) {
    if (card.name === 'Mardroeme' && row.special !== null) return 0;
    let ermion = card.holder.hand.cards.filter((c) => c.name === 'Ermion').length > 0;
    if (ermion && card.name !== 'Ermion' && row === board.row[1]) return 0;
    let name = row === board.row[1] ? 'Young Berserker' : 'Berserker';
    let n = row.cards.filter((c) => c.name === name).length;
    let weight = row === board.row[2] ? 10 * n : 8 * n * n - 2 * n;
    return Math.max(1, weight);
  }

  // Calculates the weight for cards with the medic ability
  weightMedic(data, score, owner) {
    let units = owner.grave.findCards((c) => c.isUnit());
    let grave = data['grave_' + owner.opponent().tag];
    return !units.length
      ? Math.min(1, score)
      : score +
          (grave.spy.length
            ? 50
            : grave.medic.length
              ? 15
              : grave.scorch.length
                ? 10
                : this.player.health === 1
                  ? 1
                  : 0);
  }

  // Calculates the weight for cards with the berserker ability
  weightBerserker(card, row, score) {
    if (
      card.holder.hand.cards.filter((c) => c.abilities.includes('mardroeme')).length < 1 &&
      !row.effects.mardroeme > 0
    )
      return score;
    score -= card.basePower;
    if (card.row === 'close') score += 14;
    else {
      let n = 0;
      if (!row.effects.mardroeme) n = row.cards.filter((c) => c.name === 'Young Berserker').length;
      else n = row.cards.filter((c) => 'Transformed Young Vildkaarl').length;
      score = 8 * ((n + 1) * (n + 1) - n * n) + n * score;
    }
    return Math.max(1, score);
  }

  // Calculates the weight for a weather card if played from the deck
  weightWeatherFromDeck(card, weather_id) {
    if (card.holder.deck.findCard((c) => c.abilities.includes(weather_id)) === undefined) return 0;
    return this.weightCard({ abilities: [weather_id], row: 'weather' });
  }

  // Assigns a weights for how likely the controller with play a card from its hand
  weightCard(card, max, data) {
    if (card.name === 'Decoy')
      return data.spy.length
        ? 50
        : data.medic.length
          ? 15
          : data.scorch.length
            ? 10
            : max.me.length
              ? 1
              : 0;
    if (card.name === "Commander's Horn") {
      let rows = [0, 1, 2].map((i) => board.row[i]).filter((r) => r.special === null);
      if (!rows.length) return 0;
      rows = rows.map((r) => this.weightHornRow(card, r));
      return Math.max(...rows) / 2;
    }

    if (card.abilities) {
      if (card.abilities.includes('scorch')) {
        let power_op = max.op.length ? max.op[0].card.power : 0;
        let power_me = max.me.length ? max.me[0].card.power : 0;
        let total_op = power_op * max.op.length;
        let total_me = power_me * max.me.length;
        return power_me > power_op
          ? 0
          : power_me < power_op
            ? total_op
            : Math.max(0, total_op - total_me);
      }
      if (card.abilities.includes('decoy')) {
        return data.spy.length
          ? 50
          : data.medic.length
            ? 15
            : data.scorch.length
              ? 10
              : max.me.length
                ? 1
                : 0;
      }
      if (card.abilities.includes('mardroeme')) {
        let rows = [1, 2].map((i) => board.row[i]);
        return Math.max(...rows.map((r) => this.weightMardroemeRow(card, r)));
      }
    }

    if (card.row === 'weather') {
      return Math.max(0, this.weightWeather(card));
    }

    let row;
    if (card.row === 'agile') row = this.determineAgileRow(card);
    else row = board.getRow(card, card.row === 'agile' ? 'close' : card.row, this.player);
    let score = row.calcCardScore(card);
    switch (card.abilities[card.abilities.length - 1]) {
      case 'bond':
      case 'morale':
      case 'horn':
        score = this.weightRowChange(card, row);
        break;
      case 'medic':
        score = this.weightMedic(data, score, card.holder);
        break;
      case 'spy':
        score = 15 + score;
        break;
      case 'muster':
        score *= 3;
        break;
      case 'scorch_c':
        score = Math.max(1, this.weightScorchRow(card, max, 'close'));
        break;
      case 'scorch_r':
        score = Math.max(1, this.weightScorchRow(card, max, 'ranged'));
        break;
      case 'scorch_s':
        score = Math.max(1, this.weightScorchRow(card, max, 'siege'));
        break;
      case 'berserker':
        score = this.weightBerserker(card, row, score);
        break;
      case 'avenger':
      case 'avenger_kambi':
        return score + ability_dict[card.abilities[card.abilities.length - 1]].weight();
    }

    return score;
  }

  // Calculates the current power of a row associated with each Player
  calcRowPower(r, dif, add) {
    r.findCards((c) => c.isUnit()).forEach((c) => {
      let p = r.calcCardScore(c);
      c.holder === this.player ? (dif[0] += add ? p : -p) : (dif[1] += add ? p : -p);
    });
  }
}

// Can make actions during turns like playing cards that it owns
class Player {
  constructor(id, name, deck) {
    this.id = id;
    this.tag = id === 0 ? 'me' : 'op';
    this.controller = id === 0 ? new Controller() : new ControllerAI(this);

    this.hand = id === 0 ? new Hand(document.getElementById('hand-row')) : new HandAI();
    this.grave = new Grave(document.getElementById('grave-' + this.tag));
    this.deck = new Deck(deck.faction, document.getElementById('deck-' + this.tag));
    this.deck_data = deck;

    this.leader = new Card(deck.leader, this);
    this.elem_leader = document.getElementById('leader-' + this.tag);
    this.elem_leader.children[0].appendChild(this.leader.elem);

    this.reset();

    this.name = name;
    document.getElementById('name-' + this.tag).innerHTML = name;

    document.getElementById('deck-name-' + this.tag).innerHTML = factions[deck.faction].name;
    document.getElementById('stats-' + this.tag).getElementsByClassName('profile-img')[0]
      .children[0].children[0];
    let x = document.querySelector('#stats-' + this.tag + ' .profile-img > div > div');
    x.style.backgroundImage = iconURL('deck_shield_' + deck.faction);
  }

  // Sets default values
  reset() {
    this.grave.reset();
    this.hand.reset();
    this.deck.reset();
    this.deck.initializeFromID(this.deck_data.cards, this);

    this.health = 2;
    this.total = 0;
    this.passed = false;
    this.handsize = 10;
    this.winning = false;

    this.enableLeader();
    this.setPassed(false);
    document.getElementById('gem1-' + this.tag).classList.add('gem-on');
    document.getElementById('gem2-' + this.tag).classList.add('gem-on');
  }

  // Returns the opponent Player
  opponent() {
    return board.opponent(this);
  }

  // Updates the player's total score and notifies the gamee
  updateTotal(n) {
    this.total += n;
    document.getElementById('score-total-' + this.tag).children[0].innerHTML = this.total;
    board.updateLeader();
  }

  // Puts the player in the winning state
  setWinning(isWinning) {
    if (this.winning ^ isWinning)
      document.getElementById('score-total-' + this.tag).classList.toggle('score-leader');
    this.winning = isWinning;
  }

  // Puts the player in the passed state
  setPassed(hasPassed) {
    if (this.passed ^ hasPassed)
      document.getElementById('passed-' + this.tag).classList.toggle('passed');
    this.passed = hasPassed;
  }

  // Sets up board for turn
  async startTurn() {
    document.getElementById('stats-' + this.tag).classList.add('current-turn');
    if (this.leaderAvailable) this.elem_leader.children[1].classList.remove('hide');

    if (this === player_me) {
      document.getElementById('pass-button').classList.remove('noclick');
    }

    if (this.controller instanceof ControllerAI) {
      await this.controller.startTurn(this);
    }
  }

  // Passes the round and ends the turn
  passRound() {
    this.setPassed(true);
    EventManager.roundPassed.dispatch(this, game.roundCount);
    this.endTurn();
  }

  // Plays a scorch card
  async playScorch(card) {
    await this.playCardAction(card, async () => await ability_dict['scorch'].activated(card));
  }

  // Plays a card to a specific row
  async playCardToRow(card, row) {
    await this.playCardAction(card, async () => await board.moveTo(card, row, this.hand));
  }

  // Plays a card to the board
  async playCard(card) {
    await this.playCardAction(card, async () => await card.autoplay(this.hand));
  }

  // Shows a preview of the card being played, plays it to the board and ends the turn
  async playCardAction(card, action) {
    ui.showPreviewVisuals(card);
    await sleep(1000);
    ui.hidePreview(card);
    await action();
    this.endTurn();
  }

  // Handles end of turn visuals and behavior the notifies the game
  endTurn() {
    if (!this.passed && !this.canPlay()) this.setPassed(true);
    if (this === player_me) {
      document.getElementById('pass-button').classList.add('noclick');
    }
    document.getElementById('stats-' + this.tag).classList.remove('current-turn');
    this.elem_leader.children[1].classList.add('hide');
    game.endTurn();
  }

  // Tells the the Player if it won the round. May damage health.
  endRound(win) {
    if (!win) {
      if (this.health < 1) return;
      document.getElementById('gem' + this.health + '-' + this.tag).classList.remove('gem-on');
      this.health--;
    }
    this.setPassed(false);
    this.setWinning(false);
  }

  // Returns true if the Player can make any action other than passing
  canPlay() {
    return this.hand.cards.length > 0 || this.leaderAvailable;
  }

  // Use a leader's Activate ability, then disable the leader
  async activateLeader() {
    ui.showPreviewVisuals(this.leader);
    await sleep(1500);
    ui.hidePreview(this.leader);
    await this.leader.activated[0](this.leader, this);
    this.disableLeader();
    this.endTurn();
  }

  // Disable access to leader ability and toggles leader visuals to off state
  disableLeader() {
    this.leaderAvailable = false;
    let elem = this.elem_leader.cloneNode(true);
    this.elem_leader.parentNode.replaceChild(elem, this.elem_leader);
    this.elem_leader = elem;
    this.elem_leader.children[0].classList.add('fade');
    this.elem_leader.children[1].classList.add('hide');
    this.elem_leader.addEventListener('click', async () => await ui.viewCard(this.leader), false);
    this.elem_leader.addEventListener('mouseenter', CLICK_EVENT_SFX);
    this.elem_leader.children[0].setAttribute('data-title', 'View leader');
  }

  // Enable access to leader ability and toggles leader visuals to on state
  enableLeader() {
    this.leaderAvailable = this.leader.activated.length > 0;
    let elem = this.elem_leader.cloneNode(true);
    this.elem_leader.parentNode.replaceChild(elem, this.elem_leader);
    this.elem_leader = elem;
    this.elem_leader.children[0].classList.remove('fade');
    this.elem_leader.children[1].classList.remove('hide');

    if (this.id === 0 && this.leader.activated.length > 0) {
      this.elem_leader.addEventListener(
        'click',
        async () =>
          await ui.viewCard(this.leader, async () => {
            AudioManager.playSFX('open');
            await this.activateLeader();
          }),
        false,
      );
      this.elem_leader.children[0].setAttribute('data-title', 'Play leader');
    } else {
      this.elem_leader.addEventListener('click', async () => await ui.viewCard(this.leader), false);
    }
    this.elem_leader.addEventListener('mouseenter', CLICK_EVENT_SFX);

    // TODO set crown color
  }
}

// Handles the adding, removing and formatting of cards in a container
class CardContainer {
  constructor(elem) {
    this.elem = elem;
    this.cards = [];
  }

  // Returns the first card that satisfies the predcicate. Does not modify container.
  findCard(predicate) {
    for (let i = this.cards.length - 1; i >= 0; --i)
      if (predicate(this.cards[i])) return this.cards[i];
  }

  // Returns a list of cards that satisfy the predicate. Does not modify container.
  findCards(predicate) {
    return this.cards.filter(predicate);
  }

  // Returns a list of up to n cards that satisfy the predicate. Does not modify container.
  findCardsRandom(predicate, n) {
    let valid = predicate ? this.cards.filter(predicate) : this.cards;
    if (valid.length === 0) return [];
    if (!n || n === 1) return [valid[randomInt(valid.length)]];
    let out = [];
    for (let i = Math.min(n, valid.length); i > 0; --i) {
      let index = randomInt(valid.length);
      out.push(valid.splice(index, 1)[0]);
    }
    return out;
  }

  // Removes and returns a list of cards that satisy the predicate.
  getCards(predicate) {
    return this.cards
      .reduce((a, c, i) => (predicate(c, i) ? [i] : []).concat(a), [])
      .map((i) => this.removeCard(i));
  }

  // Removes and returns a card that satisfies the predicate.
  getCard(predicate) {
    for (let i = this.cards.length - 1; i >= 0; --i)
      if (predicate(this.cards[i])) return this.removeCard(i);
  }

  // Removes and returns any cards up to n that satisfy the predicate.
  getCardsRandom(predicate, n) {
    return this.findCardsRandom(predicate, n).map((c) => this.removeCard(c));
  }

  // Adds a card to the container along with its associated HTML element.
  addCard(card, index) {
    if (!card) return;
    index = index ? clamp(0, this.cards.length, index) : 0;
    this.cards.splice(index, 0, card);
    this.addCardElement(card, index);
    this.resize();
  }

  // Removes a card from the container along with its associated HTML element.
  removeCard(card, index) {
    if (this.cards.length === 0) throw 'Cannot draw from empty ' + this.constructor.name;
    card = this.cards.splice(isNumber(card) ? card : this.cards.indexOf(card), 1)[0];
    this.removeCardElement(card, index ? index : 0);
    this.resize();
    return card;
  }

  // Adds a card to a pre-sorted CardContainer
  addCardSorted(card) {
    let i = this.getSortedIndex(card);
    this.cards.splice(i, 0, card);
    return i;
  }

  // Returns the expected index of a card in a sorted CardContainer
  getSortedIndex(card) {
    for (var i = 0; i < this.cards.length; ++i) if (Card.compare(card, this.cards[i]) < 0) break;
    return i;
  }

  // Adds a card to a random index of the CardContainer
  addCardRandom(card) {
    this.cards.push(card);
    let index = randomInt(this.cards.length);
    if (index !== this.cards.length - 1) {
      let t = this.cards[this.cards.length - 1];
      this.cards[this.cards.length - 1] = this.cards[index];
      this.cards[index] = t;
    }
    return index;
  }

  // Removes the HTML elemenet associated with the card from this CardContainer
  removeCardElement(card, index) {
    if (this.elem) this.elem.removeChild(card.elem);
  }

  // Adds the HTML elemenet associated with the card to this CardContainer
  addCardElement(card, index) {
    if (this.elem) {
      if (index === this.cards.length) this.elem.appendChild(card.elem);
      else this.elem.insertBefore(card.elem, this.elem.children[index]);
    }
  }

  // Empty function to be overried by subclasses that resize their content
  resize() {}

  // Modifies the margin of card elements inside a row-like container to stack properly
  resizeCardContainer(overlap_count, gap, coef) {
    let n = this.elem.children.length;
    let param = n < overlap_count ? '' + gap + 'vw' : defineCardRowMargin(n, coef);
    let children = this.elem.getElementsByClassName('card');
    for (let x of children) x.style.marginLeft = x.style.marginRight = param;

    function defineCardRowMargin(n, coef = 0) {
      return 'calc((100% - (4.45vw * ' + n + ')) / (2*' + n + ') - (' + coef + 'vw * ' + n + '))';
    }
  }

  // Allows the row to be clicked
  setSelectable() {
    this.elem.classList.add('row-selectable');
  }

  // Disallows teh row to be clicked
  clearSelectable() {
    this.elem.classList.remove('row-selectable');
    for (card in this.cards) card.elem.classList.add('noclick');
  }

  // Returns the container to its default, empty state
  reset() {
    while (this.cards.length) this.removeCard(0);
    if (this.elem) while (this.elem.firstChild) this.elem.removeChild(this.elem.firstChild);
    this.cards = [];
  }
}

// Contians all used cards in the order that they were discarded
class Grave extends CardContainer {
  constructor(elem) {
    super(elem);
    elem.addEventListener('click', () => ui.viewCardsInContainer(this), false);
  }

  // Override
  addCard(card) {
    this.setCardOffset(card, this.cards.length);
    if (card && this.cards.length === 0) {
      this.elem.addEventListener('mouseenter', CLICK_EVENT_SFX);
    }
    super.addCard(card, this.cards.length);
  }

  // Override
  removeCard(card) {
    let n = isNumber(card) ? card : this.cards.indexOf(card);
    if (n > -1 && this.cards.length === 1) {
      this.elem.removeEventListener('mouseenter', CLICK_EVENT_SFX);
    }
    return super.removeCard(card, n);
  }

  // Override
  removeCardElement(card, index) {
    card.elem.style.left = '';
    super.removeCardElement(card, index);
    for (let i = index; i < this.cards.length; ++i) {
      //			if (!this.cards[i])
      this.setCardOffset(this.cards[i], i);
    }
  }

  // Offsets the card element in the deck
  setCardOffset(card, n) {
    card.elem.style.left = -0.03 * n + 'vw';
  }
}

// Contains a randomized set of cards to be drawn from
class Deck extends CardContainer {
  constructor(faction, elem) {
    super(elem);
    this.faction = faction;

    this.counter = document.createElement('div');
    this.counter.classList = 'deck-counter center';
    this.counter.appendChild(document.createTextNode(this.cards.length));
    this.elem.appendChild(this.counter);
  }

  // Creates duplicates of cards with a count of more than one, then initializes deck
  initializeFromID(card_id_list, player) {
    this.initialize(Card.expandIDCounts(card_id_list), player);
  }

  // Populates a this deck with a list of card data and associated those cards with the owner of this deck.
  initialize(card_data_list, player) {
    for (let i = 0; i < card_data_list.length; ++i) {
      let card = new Card(card_data_list[i], player);
      this.addCardRandom(card);
      this.addCardElement();
    }
    this.resize();
  }

  // Override
  addCard(card) {
    this.addCardRandom(card);
    this.addCardElement();
    this.resize();
  }

  // Sends the top card to the passed hand
  async draw(hand) {
    if (hand === player_op.hand) hand.addCard(this.removeCard(0));
    else await board.toHand(this.cards[0], this);
  }

  // Draws a card and sends it to the container before adding a card from the
  // container back to the deck.
  // NOTE: Used only in mulligan and adds card out of order
  swap(container, card) {
    if (!card) return;
    const index = container.cards.indexOf(card);
    this.addCard(container.removeCard(card));
    const drawnCard = this.removeCard(0);
    container.addCard(drawnCard, index);
  }

  // Override
  addCardElement() {
    let elem = document.createElement('div');
    elem.classList.add('deck-card');
    elem.style.backgroundImage = iconURL('deck_back_' + this.faction, 'jpg');
    this.setCardOffset(elem, this.cards.length - 1);
    this.elem.insertBefore(elem, this.counter);
  }

  // Override
  removeCardElement() {
    this.elem.removeChild(this.elem.children[this.cards.length]).style.left = '';
  }

  // Offsets the card element in the deck
  setCardOffset(elem, n) {
    elem.style.left = -0.03 * n + 'vw';
  }

  // Override
  resize() {
    this.counter.innerHTML = this.cards.length;
    this.setCardOffset(this.counter, this.cards.length);
  }

  // Override
  reset() {
    super.reset();
    this.elem.appendChild(this.counter);
  }
}

// Hand used by computer AI. Has an offscreen HTML element for card transitions.
class HandAI extends CardContainer {
  constructor() {
    super(undefined);
    this.counter = document.getElementById('hand-count-op');
    this.hidden_elem = document.getElementById('hand-op');
  }
  resize() {
    this.counter.innerHTML = this.cards.length;
  }
}

// Hand used by current player
class Hand extends CardContainer {
  constructor(elem) {
    super(elem);
    this.counter = document.getElementById('hand-count-me');
  }

  // Override
  addCard(card, index) {
    const sortedIndex = this.getSortedIndex(card);
    if (!index) index = this.addCardSorted(card);
    else super.addCard(card, index);
    this.addCardElement(card, sortedIndex);
    this.resize();
  }

  // Override
  resize() {
    this.counter.innerHTML = this.cards.length;
    this.resizeCardContainer(11, 0.075, 0.00225);
  }
}

// Contains active cards and effects. Calculates the current score of each card and the row.
class Row extends CardContainer {
  constructor(elem) {
    super(elem?.getElementsByClassName('row-cards')[0]);
    this.type = elem?.getAttribute('data-row');
    this.elem_parent = elem;
    this.elem_special = elem?.getElementsByClassName('row-special')[0];
    this.special = null;
    this.total = 0;
    this.effects = {
      weather: false,
      halfWeather: false,
      bond: {},
      morale: 0,
      horn: 0,
      mardroeme: 0,
    };
    this.elem?.addEventListener('click', () => ui.selectRow(this), true);
    this.elem_special?.addEventListener('click', () => ui.selectRow(this), false, true);
  }

  // Returns a copy of the row
  getVirtualCopy(predicate = (c) => true) {
    const copy = new Row(null);
    copy.type = this.type;
    copy.effects = { ...this.effects };
    copy.effects.bond = { ...this.effects.bond };
    copy.cards = this.cards.filter(predicate);
    // remove status of filtered out cards
    this.cards.filter((c) => !predicate(c)).forEach((c) => copy.updateState(c, false));
    return copy;
  }

  // Override
  async addCard(card) {
    if (card.isSpecial()) {
      this.special = card;
      this.elem_special.appendChild(card.elem);
    } else {
      let index = this.addCardSorted(card);
      this.addCardElement(card, index);
      this.resize();
      await this.playPlacementAudio(card);
    }
    this.updateState(card, true);
    game.placedEffectsActive = true;
    for (let x of card.placed) await x(card, this);
    game.placedEffectsActive = false;
    card.elem.classList.add('noclick');
    await sleep(600);
    this.updateScore();
  }

  async playPlacementAudio(card) {
    let key;
    if (card.abilities.includes('spy') || card.abilities.includes('vildkarrl')) return;
    else if (card.abilities.includes('berserker') && this.effects.mardroeme >= 1) return;
    else if (card.abilities.includes('decoy')) key = 'decoy';
    else if (card.isHero()) {
      key = 'hero';
    } else {
      switch (this.type) {
        case 'siege':
          key = 'common_siege';
          break;
        case 'ranged':
          key = 'common_ranged';
          break;
        case 'close':
          key = 'common_close';
          break;
        default:
          return;
      }
    }
    if (key) {
      return await AudioManager.playSFX(key, DURATION_CARD_PLACEMENT, true);
    }
  }

  // Override
  removeCard(card) {
    card = isNumber(card) ? (card === -1 ? this.special : this.cards[card]) : card;
    if (card.isSpecial()) {
      this.special = null;
      this.elem_special.removeChild(card.elem);
    } else {
      super.removeCard(card);
      card.resetPower();
    }
    this.updateState(card, false);
    for (let x of card.removed) x(card);
    this.updateScore();
    return card;
  }

  // Override
  removeCardElement(card, index) {
    super.removeCardElement(card, index);
    let x = card.elem;
    x.style.marginLeft = x.style.marginRight = '';
    x.classList.remove('noclick');
  }

  // Updates a card's effect on the row
  updateState(card, activate) {
    for (let x of card.abilities) {
      switch (x) {
        case 'morale':
        case 'horn':
        case 'mardroeme':
          this.effects[x] += activate ? 1 : -1;
          break;
        case 'bond':
          if (!this.effects.bond[card.id()]) this.effects.bond[card.id()] = 0;
          this.effects.bond[card.id()] += activate ? 1 : -1;
          break;
      }
    }
  }

  // Activates weather effect and visuals
  addOverlay(overlay) {
    this.effects.weather = true;
    const elem = this.elem_parent.getElementsByClassName('row-weather')[0];
    elem.classList.add(overlay);
    fadeIn(elem, 500);
    this.updateScore();
  }

  // Deactivates weather effect and visuals
  removeOverlay(overlay) {
    this.effects.weather = false;
    const elem = this.elem_parent.getElementsByClassName('row-weather')[0];
    fadeOut(elem, 500).then(() => elem.classList.remove(overlay));
    this.updateScore();
  }

  // Override
  resize() {
    this.resizeCardContainer(10, 0.075, 0.00325);
  }

  // Updates the row's score by summing the current power of its cards
  updateScore() {
    let total = 0;
    for (let card of this.cards) {
      total += this.cardScore(card);
    }
    let player = this.elem_parent.parentElement.id === 'field-op' ? player_op : player_me;
    player.updateTotal(total - this.total);
    this.total = total;
    this.elem_parent.getElementsByClassName('row-score')[0].innerHTML = this.total;
  }

  // Calculates and set the card's current power
  cardScore(card) {
    let total = this.calcCardScore(card);
    card.setPower(total);
    return total;
  }

  // Calculate total row score without updating
  calcScore() {
    return this.cards.reduce((sum, card) => sum + this.calcCardScore(card), 0);
  }

  // Calculates the current power of a card affected by row affects
  calcCardScore(card) {
    if (card.name === 'decoy') return 0;
    let total = card.basePower;
    if (card.hero) return total;
    if (this.effects.weather) {
      const weatherMin = this.effects.halfWeather ? Math.floor(total / 2) : 1;
      total = Math.min(weatherMin, total);
    }
    if (game.doubleSpyPower && card.abilities.includes('spy')) total *= 2;
    let bond = this.effects.bond[card.id()];
    if (isNumber(bond) && bond > 1) total *= Number(bond);
    total += Math.max(0, this.effects.morale + (card.abilities.includes('morale') ? -1 : 0));
    if (this.effects.horn - (card.abilities.includes('horn') ? 1 : 0) > 0) total *= 2;
    return total;
  }

  // Applies a temporary leader horn affect that is removed at the end of the round
  async leaderHorn() {
    if (this.special !== null) return;
    let horn = new Card(card_dict[5], null);
    await this.addCard(horn);
    game.roundEnd.push(() => this.removeCard(horn));
  }

  // Applies a local scorch effect to this row
  async scorch() {
    if (this.total >= 10)
      await Promise.all(
        this.maxUnits().map(async (c) => {
          await c.animate('scorch', true, false);
          await board.toGrave(c, this);
        }),
      );
  }

  // Removes all cards and effects from this row
  async clear() {
    const toGrave = this.cards.filter((c) => !c.noRemove);
    if (this.special != null) toGrave.push(this.special);
    await Promise.all(toGrave.map(async (c) => await board.toGrave(c, this)));
  }

  // Returns all regular unit cards with the heighest power
  maxUnits() {
    let max = [];
    for (let i = 0; i < this.cards.length; ++i) {
      let card = this.cards[i];
      if (!card.isUnit()) continue;
      if (!max[0] || max[0].power < card.power) max = [card];
      else if (max[0].power === card.power) max.push(card);
    }
    return max;
  }

  // Override
  reset() {
    super.reset();
    while (this.special) this.removeCard(this.special);
    while (this.elem_special.firstChild)
      this.elem_special.removeChild(this.elem_special.firstChild);
    this.total = 0;
    //["rain","fog","frost"].forEach( w => this.removeOverlay(w) );
    this.effects = { weather: false, bond: {}, morale: 0, horn: 0, mardroeme: 0 };
  }
}

// Handles how weather effects are added and removed
class Weather extends CardContainer {
  constructor(elem) {
    super(document.getElementById('weather'));
    this.types = {
      rain: { name: 'rain', count: 0, rows: [] },
      fog: { name: 'fog', count: 0, rows: [] },
      frost: { name: 'frost', count: 0, rows: [] },
    };
    let i = 0;
    for (let key of Object.keys(this.types))
      this.types[key].rows = [board.row[i], board.row[5 - i++]];

    this.elem.addEventListener('click', () => ui.selectRow(this), false);
  }

  // Adds a card if unique and clears all weather if 'clear weather' card added
  async addCard(card) {
    const isDuplicate = !!this.cards.find((c) => c.name === card.name);
    super.addCard(card);
    AudioManager.playSFX(card.audio);
    card.elem.classList.add('noclick');
    if (card.name === 'Clear Weather') {
      // TODO Sunlight animation
      await sleep(500);
      this.clearWeather();
    } else {
      this.changeWeather(
        card,
        (x) => ++this.types[x].count === 1,
        (r, t) => r.addOverlay(t.name),
      );
      if (isDuplicate) {
        await sleep(750);
        await board.toGrave(card, this);
      }
    }
    await sleep(1000);
  }

  // Override
  removeCard(card) {
    card = super.removeCard(card);
    card.elem.classList.remove('noclick');
    this.changeWeather(
      card,
      (x) => --this.types[x].count === 0,
      (r, t) => r.removeOverlay(t.name),
    );
    return card;
  }

  // Checks if a card's abilities are a weather type. If the predicate is met, perfom the action
  // on the type's associated rows
  changeWeather(card, predicate, action) {
    for (let x of card.abilities) {
      if (x in this.types && predicate(x)) {
        for (let r of this.types[x].rows) action(r, this.types[x]);
      }
    }
  }

  // Removes all weather effects and cards
  async clearWeather() {
    await Promise.all(
      this.cards
        .map((c, i) => this.cards[this.cards.length - i - 1])
        .map(async (c) => await board.toGrave(c, this)),
    );
  }

  // Override
  resize() {
    this.resizeCardContainer(4, 0.075, 0.045);
  }

  // Override
  reset() {
    super.reset();
    Object.keys(this.types).map((t) => (this.types[t].count = 0));
  }
}

//
class Board {
  constructor() {
    this.op_score = 0;
    this.me_score = 0;
    this.row = [];
    for (let x = 0; x < 6; ++x) {
      let elem = document.getElementById(x < 3 ? 'field-op' : 'field-me').children[x % 3];
      this.row[x] = new Row(elem);
    }
  }

  // Get the opponent of this Player
  opponent(player) {
    return player === player_me ? player_op : player_me;
  }

  // Sends and translates a card from the source to the Deck of the card's holder
  async toDeck(card, source) {
    await this.moveTo(card, 'deck', source);
  }

  // Sends and translates a card from the source to the Grave of the card's holder
  async toGrave(card, source) {
    await this.moveTo(card, 'grave', source);
  }

  // Sends and translates a card from the source to the Hand of the card's holder
  async toHand(card, source) {
    await this.moveTo(card, 'hand', source);
  }

  // Sends and translates a card from the source to Weather
  async toWeather(card, source) {
    await this.moveTo(card, weather, source);
  }

  // Sends and translates a card from the source to the Deck of the card's combat row
  async toRow(card, source) {
    let row = card.row;
    if (row === 'agile') {
      if (card.holder.controller instanceof ControllerAI) {
        row = card.holder.controller.determineAgileRow(card).type;
      } else {
        row = 'close';
      }
    }
    await this.moveTo(card, row, source);
  }

  // Sends and translates a card from the source to a specified row name or CardContainer
  async moveTo(card, dest, source) {
    if (isString(dest)) dest = this.getRow(card, dest);
    await translateTo(card, source ? source : null, dest);
    await dest.addCard(source ? source.removeCard(card) : card);
  }

  // Sends and translates a card from the source to a row name associated with the passed player
  async addCardToRow(card, row_name, player, source) {
    let row = this.getRow(card, row_name, player);
    await translateTo(card, source, row);
    await row.addCard(card);
  }

  // Returns the CardCard associated with the row name that the card would be sent to
  getRow(card, row_name, player) {
    player = player ? player : card ? card.holder : player_me;
    let isMe = player === player_me;
    let isSpy = card.abilities.includes('spy');
    switch (row_name) {
      case 'weather':
        return weather;
        break;
      case 'close':
        return this.row[isMe ^ isSpy ? 3 : 2];
      case 'ranged':
        return this.row[isMe ^ isSpy ? 4 : 1];
      case 'siege':
        return this.row[isMe ^ isSpy ? 5 : 0];
      case 'grave':
        return player.grave;
      case 'deck':
        return player.deck;
      case 'hand':
        return player.hand;
      default:
        console.error(
          card.name + ' sent to incorrect row "' + row_name + '" by ' + card.holder.name,
        );
    }
  }

  // Updates which player currently is in the lead
  updateLeader() {
    let dif = player_me.total - player_op.total;
    player_me.setWinning(dif > 0);
    player_op.setWinning(dif < 0);
  }

  async clearRound() {
    await Promise.all([
      await weather.clearWeather(),
      ...board.row.map(async (row) => await row.clear()),
    ]);
  }
}

class GameStateEnum extends Enum {}
const GameState = Object.freeze({
  CUSTOMIZE: new GameStateEnum(0),
  PLAYING: new GameStateEnum(10),
  END_SCREEN: new GameStateEnum(100),
});

class Game {
  constructor() {
    this.endScreen = document.getElementById('end-screen');
    let buttons = this.endScreen.getElementsByTagName('button');
    this.customize_elem = buttons[0];
    this.rematch_elem = buttons[1];
    this.newGame_elem = buttons[2];
    this.customize_elem.addEventListener('click', () => this.returnToCustomization(), false);
    this.rematch_elem.addEventListener('click', () => this.rematchGame(), false);
    this.newGame_elem.addEventListener('click', () => this.newOpponentGame(), false);
    this.state = GameState.CUSTOMIZE;
    this.reset();
  }

  reset() {
    this.firstPlayer = null;
    this.currPlayer = null;

    this.gameStart = [];
    this.roundStart = [];
    this.roundEnd = [];
    this.turnStart = [];
    this.turnEnd = [];

    this.roundCount = 0;
    this.roundHistory = [];

    this.randomRespawn = false;
    this.doubleSpyPower = false;

    this.placedEffectsActive = false; //TODO replace with propper game state

    weather.reset();
    board.row.forEach((r) => r.reset());
  }

  // Sets up player faction abilities and psasive leader abilities
  initPlayers(p1, p2) {
    let l1 = ability_dict[p1.leader.abilities[0]];
    let l2 = ability_dict[p2.leader.abilities[0]];
    if (l1 === ability_dict['emhyr_whiteflame'] || l2 === ability_dict['emhyr_whiteflame']) {
      p1.disableLeader();
      p2.disableLeader();
    } else {
      initLeader(p1, l1);
      initLeader(p2, l2);
    }
    if (p1.deck.faction === p2.deck.faction && p1.deck.faction === 'scoiatael') return;
    initFaction(p1);
    initFaction(p2);

    function initLeader(player, leader) {
      if (leader.placed) leader.placed(player.leader);
      Object.keys(leader)
        .filter((key) => game[key])
        .map((key) => game[key].push(leader[key]));
    }

    function initFaction(player) {
      if (factions[player.deck.faction] && factions[player.deck.faction].factionAbility)
        factions[player.deck.faction].factionAbility(player);
    }
  }

  isPlaying() {
    return this.state === GameState.END_SCREEN;
  }

  setState(newState) {
    if (!(newState instanceof GameStateEnum) || this.state === newState) return;
    const oldState = this.state;
    this.state = newState;
    EventManager.gameStateChanged.dispatch(oldState, newState);
  }

  // Sets initializes player abilities, player hands and redraw
  async startGame() {
    EventManager.gameOpened.dispatch();
    this.initPlayers(player_me, player_op);
    this.setState(GameState.PLAYING);
    AudioManager.playSFX('game_opening');
    await this.runEffects(this.gameStart);
    await this.coinToss();
    AudioManager.playSFX('redraw');
    await Promise.all(
      [...Array(10).keys()].map(async () => {
        await player_me.deck.draw(player_me.hand);
        await player_op.deck.draw(player_op.hand);
      }),
    );
    AudioManager.playSFX('game_start');
    await this.initialRedraw();
    this.currPlayer = this.firstPlayer;
    this.startRound();
  }

  // Simulated coin toss to determine who starts game
  async coinToss() {
    if (this.firstPlayer) return;
    this.firstPlayer = Math.random() < 0.5 ? player_me : player_op;
    await ui.notification(this.firstPlayer.tag + '-coin', 1200);
  }

  // Allows the player to swap out up to two cards from their iniitial hand
  async initialRedraw() {
    for (let i = 0; i < 2; i++) player_op.controller.redraw();
    await ui.queueCarousel(
      player_me.hand,
      2,
      async (c, i) => {
        AudioManager.playSFX('redraw');
        await player_me.deck.swap(c, c.cards[i]);
      },
      (c) => true,
      false,
      true,
      'Choose up to 2 cards to redraw.',
    );
    ui.enablePlayer(false);
  }

  // Initiates a new round of the game
  async startRound() {
    this.firstPlayer = this.currPlayer;
    this.roundCount++;
    EventManager.roundStarted.dispatch(this.roundCount, this.currPlayer);
    if (this.roundCount === 1) AudioManager.playSFX('round1_start');
    await this.runEffects(this.roundStart);

    if (!player_me.canPlay()) player_me.setPassed(true);
    if (!player_op.canPlay()) player_op.setPassed(true);

    if (player_op.passed && player_me.passed) return this.endRound();

    if (this.currPlayer.passed) this.currPlayer = this.currPlayer.opponent();

    await ui.notification('round-start', 1200);
    AudioManager.playSFX(this.currPlayer === player_me ? 'turn_me' : 'turn_op');
    await ui.notification(this.currPlayer.tag + '-turn', 1200);
    this.startTurn();
  }

  // Starts a new turn. Enables client interraction in client's turn.
  async startTurn() {
    await this.runEffects(this.turnStart);
    ui.enablePlayer(this.currPlayer === player_me);
    this.currPlayer.startTurn();
  }

  // Ends the current turn and may end round. Disables client interraction in client's turn.
  async endTurn() {
    if (this.currPlayer === player_me) ui.enablePlayer(false);
    await this.runEffects(this.turnEnd);
    if (this.currPlayer.passed) await ui.notification(this.currPlayer.tag + '-pass', 1200);
    if (player_op.passed && player_me.passed) this.endRound();
    else {
      if (!this.currPlayer.opponent().passed) {
        this.currPlayer = this.currPlayer.opponent();
        AudioManager.playSFX(this.currPlayer === player_me ? 'turn_me' : 'turn_op');
        await ui.notification(this.currPlayer.tag + '-turn', 1200);
      }
      await this.startTurn();
    }
  }

  // Ends the round and may end the game. Determines final scores and the round winner.
  async endRound() {
    let dif = player_me.total - player_op.total;
    if (dif === 0) {
      let nilf_me = player_me.deck.faction === 'nilfgaard',
        nilf_op = player_op.deck.faction === 'nilfgaard';
      dif = nilf_me ^ nilf_op ? (nilf_me ? 1 : -1) : 0;
    }
    let winner = dif > 0 ? player_me : dif < 0 ? player_op : null;
    // Arena can map its factions to the classic tie perk without changing their IDs or scores.
    if (typeof this.arenaRoundDifference === 'function') {
      dif = this.arenaRoundDifference(player_me, player_op);
      winner = dif > 0 ? player_me : dif < 0 ? player_op : null;
    }
    let verdict = { winner: winner, score_me: player_me.total, score_op: player_op.total };
    this.roundHistory.push(verdict);

    await this.runEffects(this.roundEnd);

    player_me.endRound(dif > 0);
    player_op.endRound(dif < 0);

    let notificationKey = '';
    if (dif > 0) {
      AudioManager.playSFX('round_win');
      notificationKey = 'win-round';
    } else if (dif < 0) {
      AudioManager.playSFX('round_lose');
      notificationKey = 'lose-round';
    } else {
      AudioManager.playSFX('round_lose');
      notificationKey = 'draw-round';
    }

    await Promise.all([await board.clearRound(), await ui.notification(notificationKey, 1200)]);

    EventManager.roundEnded.dispatch(this.roundCount, player_me.total, player_op.total);
    if (player_me.health === 0 || player_op.health === 0) this.endGame();
    else {
      this.currPlayer = dif < 0 ? player_op : dif > 0 ? player_me : this.firstPlayer;
      this.startRound();
    }
  }

  // Sets up and displays the end-game screen
  async endGame() {
    let endScreen = document.getElementById('end-screen');
    let rows = endScreen.getElementsByTagName('tr');
    rows[1].children[0].innerHTML = player_me.name;
    rows[2].children[0].innerHTML = player_op.name;

    for (let i = 1; i < 4; ++i) {
      let round = this.roundHistory[i - 1];
      rows[1].children[i].innerHTML = round ? round.score_me : 0;
      rows[1].children[i].style.color = round && round.winner === player_me ? 'goldenrod' : '';

      rows[2].children[i].innerHTML = round ? round.score_op : 0;
      rows[2].children[i].style.color = round && round.winner === player_op ? 'goldenrod' : '';
    }

    endScreen.children[0].className = '';
    if (player_op.health <= 0 && player_me.health <= 0) {
      endScreen.getElementsByTagName('p')[0].classList.remove('hide');
      AudioManager.playSFX('game_lose');
      endScreen.children[0].classList.add('end-draw');
    } else if (player_op.health === 0) {
      AudioManager.playSFX('game_win');
      endScreen.children[0].classList.add('end-win');
    } else {
      AudioManager.playSFX('game_lose');
      endScreen.children[0].classList.add('end-lose');
    }

    fadeIn(endScreen, 300);
    ui.enablePlayer(true);
    this.setState(GameState.END_SCREEN);
  }

  exitGame() {
    AudioManager.playSFX('warning');
    ui.popup(
      'Resume',
      () => {},
      'Exit',
      () => this.returnToCustomization(),
      'Quit curent game?',
      'This will return you to the deck customization menu.',
    );
  }

  // Returns the client to the deck customization screen
  returnToCustomization() {
    this.reset();
    player_me.reset();
    player_op.reset();
    EventManager.customizationOpened.dispatch();
    this.endScreen.classList.add('hide');
    document.getElementById('deck-customization').classList.remove('hide');
    AudioManager.playSFX('menu_opening');
    this.setState(GameState.CUSTOMIZE);
  }

  newOpponentGame() {
    this.reset();
    player_me.reset();
    player_op = new Player('op', 'Player 2', dm.constructOpponentDeck(false));
    this.endScreen.classList.add('hide');
    this.startGame();
  }

  // Restarts the last game with the dame decks
  rematchGame() {
    this.reset();
    player_me.reset();
    player_op.reset();
    this.endScreen.classList.add('hide');
    this.startGame();
  }

  // Executes effects in list. If effect returns true, effect is removed.
  async runEffects(effects) {
    for (let i = effects.length - 1; i >= 0; --i) {
      let effect = effects[i];
      if (await effect()) effects.splice(i, 1);
    }
  }
}

// Contians information and behavior of a Card
class Card {
  constructor(card_data, player) {
    this.name = card_data.name;
    this.basePower = this.power = Number(card_data.strength);
    this.faction = card_data.deck;
    this.abilities = card_data.ability === '' ? [] : card_data.ability.split(' ');
    this.row = card_data.deck === 'weather' ? card_data.deck : card_data.row;
    this.filename = card_data.filename;
    if (card_data.muster) {
      this.muster = card_data.muster;
    }
    this.placed = [];
    this.removed = [];
    this.activated = [];
    this.holder = player;

    this.hero = false;
    if (this.abilities.length > 0) {
      this.audio = this.abilities[this.abilities.length - 1];
      if (this.abilities[0] === 'hero') {
        this.hero = true;
        this.abilities.splice(0, 1);
      }
      for (let x of this.abilities) {
        let ab = ability_dict[x];
        if ('placed' in ab) this.placed.push(ab.placed);
        if ('removed' in ab) this.removed.push(ab.removed);
        if ('activated' in ab) this.activated.push(ab.activated);
      }
    }

    if (this.row === 'leader') this.desc_name = 'Leader Ability';
    else if (this.abilities.length > 0)
      this.desc_name = ability_dict[this.abilities[this.abilities.length - 1]].name;
    else if (this.row === 'agile') this.desc_name = 'agile';
    else if (this.hero) this.desc_name = 'hero';
    else this.desc_name = '';

    this.desc = this.row === 'agile' ? ability_dict['agile'].description : '';
    for (let i = this.abilities.length - 1; i >= 0; --i) {
      this.desc += ability_dict[this.abilities[i]].description;
    }
    if (this.hero) this.desc += ability_dict['hero'].description;

    this.elem = this.createCardElem(this);
  }

  // Returns the identifier for this type of card
  id() {
    return this.name;
  }

  // Sets and displays the current power of this card
  setPower(n) {
    if (this.name === 'Decoy') return;
    let elem = this.elem.children[0].children[0];
    if (n !== this.power) {
      this.power = n;
      elem.innerHTML = this.power;
    }
    elem.style.color = n > this.basePower ? 'goldenrod' : n < this.basePower ? 'red' : '';
  }

  // Resets the power of this card to default
  resetPower() {
    this.setPower(this.basePower);
  }

  // Automatically sends and translates this card to its apropriate row from the passed source
  async autoplay(source) {
    await board.toRow(this, source);
  }

  // Animates an ability effect
  async animate(name, bFade = true, bExpand = true) {
    AudioManager.playSFX(name);
    if (name === 'scorch') {
      return await this.scorch(name);
    }
    let anim = this.elem.children[3];
    anim.style.backgroundImage = iconURL('anim_' + name);
    await sleep(50);

    if (bFade) fadeIn(anim, 300);
    if (bExpand) anim.style.backgroundSize = '100% auto';
    await sleep(300);

    if (bExpand) anim.style.backgroundSize = '80% auto';
    await sleep(1000);

    if (bFade) fadeOut(anim, 300);
    if (bExpand) anim.style.backgroundSize = '40% auto';
    await sleep(300);

    anim.style.backgroundImage = '';
  }

  // Animates the scorch effect
  async scorch(name) {
    let anim = this.elem.children[3];
    anim.style.backgroundSize = 'cover';
    anim.style.backgroundImage = iconURL('anim_' + name);
    await sleep(50);

    fadeIn(anim, 300);
    await sleep(1300);

    fadeOut(anim, 300);
    await sleep(300);

    anim.style.backgroundSize = '';
    anim.style.backgroundImage = '';
  }

  // Returns true if this is a combat card that is not a Hero
  isUnit() {
    return (
      !this.hero &&
      (this.row === 'close' ||
        this.row === 'ranged' ||
        this.row === 'siege' ||
        this.row === 'agile')
    );
  }

  // Returns true if card is sent to a Row's special slot
  isSpecial() {
    return this.name === "Commander's Horn" || this.name === 'Mardroeme';
  }

  isHero() {
    return this.hero;
  }

  // Compares by type then power then name
  static compare(a, b) {
    var dif = factionRank(a) - factionRank(b);
    if (dif !== 0) return dif;
    dif = a.basePower - b.basePower;
    if (dif && dif !== 0) return dif;
    return a.name.localeCompare(b.name);

    function factionRank(c) {
      return c.faction === 'special' ? -2 : c.faction === 'weather' ? -1 : 0;
    }
  }

  // Creates an HTML element based on the card's properties
  createCardElem(card) {
    let elem = document.createElement('div');
    elem.style.backgroundImage = smallURL(card.faction + '_' + card.filename);
    elem.classList.add('card');
    elem.setAttribute('data-title', card.name);
    elem.addEventListener('click', () => ui.selectCard(card), false);

    if (card.row === 'leader') return elem;

    let power = document.createElement('div');
    elem.appendChild(power);
    let bg;
    if (card.hero) {
      bg = 'power_hero';
      elem.classList.add('hero');
    } else if (card.faction === 'weather') {
      bg = 'power_' + card.abilities[0];
    } else if (card.faction === 'special') {
      bg = 'power_' + card.abilities[0];
      elem.classList.add('special');
    } else {
      bg = 'power_normal';
    }
    power.style.backgroundImage = iconURL(bg);

    let row = document.createElement('div');
    elem.appendChild(row);
    if (
      card.row === 'close' ||
      card.row === 'ranged' ||
      card.row === 'siege' ||
      card.row === 'agile'
    ) {
      let num = document.createElement('div');
      num.appendChild(document.createTextNode(card.basePower));
      num.classList.add('center');
      power.appendChild(num);
      row.style.backgroundImage = iconURL('card_row_' + card.row);
    }

    let abi = document.createElement('div');
    elem.appendChild(abi);
    if (card.faction !== 'special' && card.faction !== 'weather' && card.abilities.length > 0) {
      let str = card.abilities[card.abilities.length - 1];
      if (str === 'cerys') str = 'muster';
      if (str.startsWith('avenger')) str = 'avenger';
      if (str === 'scorch_c' || str == 'scorch_r' || str === 'scorch_s') str = 'scorch';
      abi.style.backgroundImage = iconURL('card_ability_' + str);
    } else if (card.row === 'agile') abi.style.backgroundImage = iconURL('card_ability_' + 'agile');

    elem.appendChild(document.createElement('div')); // animation overlay
    elem.addEventListener('mouseenter', CLICK_EVENT_SFX);
    return elem;
  }

  // Takes ID-Count object pairs and expands to a list of corresponding IDs
  static expandIDCounts(card_id_list) {
    return card_id_list.reduce((a, c) => a.concat(clone(c.count, card_dict[c.index])), []);
    function clone(n, elem) {
      for (var i = 0, a = []; i < n; ++i) a.push(elem);
      return a;
    }
  }

  // Takes ID-Count object pairs and returns a list of corresponding Cards
  static getCardsFromIdCounts(card_id_list, player) {
    return Card.expandIDCounts(card_id_list).map((e) => new Card(e, player));
  }
}

// Handles notifications and client interration with menus
class UI {
  constructor() {
    this.carousels = [];
    this.notif_elem = document.getElementById('notification-bar');
    document.getElementById('exit-game').addEventListener('click', () => game.exitGame(), false);
    this.preview = document.getElementsByClassName('card-preview')[0];
    this.previewCard = null;
    this.lastRow = null;
    this.toggleSettings = [];
    document.getElementById('pass-button').addEventListener(
      'click',
      () => {
        player_me.passRound();
        AudioManager.playSFX('pass');
      },
      false,
    );
    document.getElementById('click-background').addEventListener('click', () => ui.cancel(), false);

    EventManager.gameOpened.bind(() =>
      this.toggleSettings.forEach((e) => e.classList.remove('deck-menu')),
    );
    EventManager.customizationOpened.bind(() =>
      this.toggleSettings.forEach((e) => e.classList.add('deck-menu')),
    );

    [
      '.settings-button',
      '.deck-options',
      '#pass-button',
      '#end-screen>button',
      '#op-preview-leader',
      '#opponent-preview button',
    ].forEach(addMouseEnterSFXBySelector);
  }

  // Enables or disables client interration
  enablePlayer(enable) {
    let main = document.getElementsByTagName('main')[0].classList;
    if (enable) main.remove('noclick');
    else main.add('noclick');
  }

  // Called when the player selects a selectable card
  async selectCard(card) {
    let row = this.lastRow;
    let pCard = this.previewCard;
    if (card === pCard) return;
    if (pCard === null || card.holder.hand.cards.includes(card)) {
      this.setSelectable(null, false);
      this.showPreview(card);
    } else if (pCard.name === 'Decoy') {
      this.hidePreview(card);
      this.enablePlayer(false);
      board.toHand(card, row);
      await board.moveTo(pCard, row, pCard.holder.hand);
      pCard.holder.endTurn();
    }
  }

  // Called when the player selects a selectable CardContainer
  async selectRow(row) {
    EventManager.rowSelected.dispatch(row, player_me);
    if (game.placedEffectsActive) {
      return;
    }
    this.lastRow = row;
    if (this.previewCard === null) {
      await ui.viewCardsInContainer(row);
      return;
    }
    if (this.previewCard.name === 'Decoy') return;
    let card = this.previewCard;
    let holder = card.holder;
    this.hidePreview();
    this.enablePlayer(false);
    if (card.name === 'Scorch') {
      this.hidePreview();
      await ability_dict['scorch'].activated(card);
    } else if (card.name === 'Decoy') {
      return;
    } else {
      await board.moveTo(card, row, card.holder.hand);
    }
    holder.endTurn();
  }

  // Called when the client cancels out of a card-preview
  cancel() {
    this.hidePreview();
    EventManager.previewCancelled.dispatch();
  }

  // Displays a card preview then enables and highlights potential card destinations
  showPreview(card, allowClose = true) {
    this.showPreviewVisuals(card);
    this.setSelectable(card, true);
    AudioManager.playSFX('open');
    if (allowClose) {
      document.getElementById('click-background').classList.remove('noclick');
    } else {
      document.getElementById('leader-me').classList.add('noclick');
      document.getElementById('pass-button').classList.add('noclick');
      player_me.hand.cards.forEach((c) => c.elem.classList.add('noclick'));
    }
  }

  // Sets up the graphics and description for a card preview
  showPreviewVisuals(card) {
    this.previewCard = card;
    this.preview.classList.remove('hide');
    this.preview.getElementsByClassName('card-lg')[0].style.backgroundImage = largeURL(
      card.faction + '_' + card.filename,
    );
    let desc_elem = this.preview.getElementsByClassName('card-description')[0];
    this.setDescription(card, desc_elem);
  }

  // Hides the card preview then disables and removes highlighting from card destinations
  hidePreview() {
    document.getElementById('click-background').classList.add('noclick');
    player_me.hand.cards.forEach((c) => c.elem.classList.remove('noclick'));
    document.getElementById('leader-me').classList.remove('noclick');
    document.getElementById('pass-button').classList.remove('noclick');

    this.preview.classList.add('hide');
    this.setSelectable(null, false);
    this.previewCard = null;
    this.lastRow = null;
  }

  // Sets up description window for a card
  setDescription(card, desc) {
    if (!card) {
      desc.children[1].innerHTML = 'NULL';
      desc.children[2].innerHTML = '';
      return;
    }
    if (
      card.hero ||
      card.row === 'agile' ||
      card.abilities.length > 0 ||
      card.faction === 'faction'
    ) {
      desc.classList.remove('hide');
      let str = card.row === 'agile' ? 'agile' : '';
      if (card.abilities.length) str = card.abilities[card.abilities.length - 1];
      if (str === 'cerys') str = 'muster';
      if (str.startsWith('avenger')) str = 'avenger';
      if (str === 'scorch_c' || str == 'scorch_r' || str === 'scorch_s') str = 'scorch';
      if (
        card.row === 'leader' ||
        card.faction === 'faction' ||
        (card.abilities.length === 0 && card.row !== 'agile')
      )
        desc.children[0].style.backgroundImage = '';
      else desc.children[0].style.backgroundImage = iconURL('card_ability_' + str);
      desc.children[1].innerHTML = card.desc_name;
      desc.children[2].innerHTML = card.desc;
    } else {
      desc.classList.add('hide');
    }
  }

  // Displayed a timed notification to the client
  async notification(name, duration) {
    if (!Settings.notifications.isEnabled()) return;
    if (!duration) duration = 1200;
    const fadeSpeed = 150;
    duration = Math.max(400, duration - 2 * fadeSpeed);
    this.notif_elem.children[0].id = 'notif-' + name;
    await fadeIn(this.notif_elem, fadeSpeed);
    await sleep(duration);
    await fadeOut(this.notif_elem, fadeSpeed);
  }

  // Displays a cancellable Carousel for a single card
  async viewCard(card, action) {
    if (card === null) return;
    let container = new CardContainer();
    container.cards.push(card);
    await this.viewCardsInContainer(container, action);
  }

  // Displays a cancellable Carousel for all cards in a container
  async viewCardsInContainer(container, action) {
    action = action
      ? action
      : function () {
          return this.cancel();
        };
    await this.queueCarousel(container, 1, action, () => true, false, true);
  }

  // Displays a Carousel menu of filtered container items that match the predicate.
  // Suspends gameplay until the Carousel is closed. Automatically picks random card if activated for AI player
  async queueCarousel(container, count, action, predicate, bSort, bQuit, title) {
    if (game.currPlayer === player_op) {
      if (player_op.controller instanceof ControllerAI)
        for (let i = 0; i < count; ++i) {
          let cards = container.cards.reduce(
            (a, c, i) => (!predicate || predicate(c) ? a.concat([i]) : a),
            [],
          );
          if (cards.length === 0) break;
          await action(container, cards[randomInt(cards.length)]);
        }
      return;
    }
    let carousel = new Carousel(container, count, action, predicate, bSort, bQuit, title);
    if (Carousel.curr === undefined || Carousel.curr === null) carousel.start();
    else {
      this.carousels.push(carousel);
      return;
    }
    await sleepUntil(() => this.carousels.length === 0 && !Carousel.curr, 100);
  }

  // Starts the next queued Carousel
  quitCarousel() {
    if (this.carousels.length > 0) {
      this.carousels.shift().start();
    }
  }

  // Displays a custom confirmation menu
  async popup(yesName, yes, noName, no, title, description, alpha = 0.95) {
    let p = new Popup(yesName, yes, noName, no, title, description, alpha);
    await sleepUntil(() => !Popup.curr);
  }

  // Enables or disables selection and highlighting of rows specific to the card
  setSelectable(card, enable) {
    if (!enable) {
      for (let row of board.row) {
        row.elem.classList.remove('row-selectable');
        row.elem.classList.remove('noclick');
        row.elem_special.classList.remove('row-selectable');
        row.elem_special.classList.remove('noclick');
        row.elem.classList.add('card-selectable');

        for (let card of row.cards) {
          card.elem.classList.add('noclick');
        }
      }
      weather.elem.classList.remove('row-selectable');
      weather.elem.classList.remove('noclick');
      return;
    }
    if (card.faction === 'weather') {
      for (let row of board.row) {
        row.elem.classList.add('noclick');
        row.elem_special.classList.add('noclick');
      }
      weather.elem.classList.add('row-selectable');
      return;
    }

    weather.elem.classList.add('noclick');

    if (card.name === 'Scorch') {
      for (let r of board.row) {
        r.elem.classList.add('row-selectable');
        r.elem_special.classList.add('row-selectable');
      }
      return;
    }
    if (card.isSpecial()) {
      for (let i = 0; i < 6; i++) {
        let r = board.row[i];
        if (i < 3 || r.special !== null) {
          r.elem.classList.add('noclick');
          r.elem_special.classList.add('noclick');
        } else {
          r.elem_special.classList.add('row-selectable');
        }
      }
      return;
    }

    board.row.forEach((r) => r.elem_special.classList.add('noclick'));

    if (card.name === 'Decoy') {
      for (let i = 0; i < 6; ++i) {
        let r = board.row[i];
        let units = r.cards.filter((c) => c.isUnit());
        if (i < 3 || units.length === 0) {
          r.elem.classList.add('noclick');
          r.elem_special.classList.add('noclick');
          r.elem.classList.remove('card-selectable');
        } else {
          r.elem.classList.add('row-selectable');
          units.forEach((c) => c.elem.classList.remove('noclick'));
        }
      }
      return;
    }

    let currRows =
      card.row === 'agile'
        ? [board.getRow(card, 'close', card.holder), board.getRow(card, 'ranged', card.holder)]
        : [board.getRow(card, card.row, card.holder)];
    for (let i = 0; i < 6; i++) {
      let row = board.row[i];
      if (currRows.includes(row)) {
        row.elem.classList.add('row-selectable');
      } else {
        row.elem.classList.add('noclick');
      }
    }
  }

  // used to handle row selection when resetoring agile units via medics
  async waitForRowSelection(card) {
    game.placedEffectsActive = true;
    ui.setSelectable(null, false);
    ui.showPreview(card, false);
    ui.enablePlayer(true);
    let selectedRow = null;
    let bRowSelected = false;
    const rowSelect = (event) => {
      const { row, player } = event.detail;
      bRowSelected = true;
      selectedRow = row;
    };
    EventManager.rowSelected.bind(rowSelect);
    EventManager.previewCancelled.bind(rowSelect);
    await sleepUntil(() => bRowSelected === true);
    EventManager.rowSelected.unbind(rowSelect);
    EventManager.previewCancelled.unbind(rowSelect);
    ui.hidePreview();
    game.placedEffectsActive = false;
    return selectedRow;
  }
}

// Displays up to 5 cards for the client to cycle through and select to perform an action
// Clicking the middle card performs the action on that card "count" times
// Clicking adejacent cards shifts the menu to focus on that card
class Carousel {
  constructor(container, count, action, predicate, bSort, bExit = false, title) {
    if (count <= 0 || !container || !action || container.cards.length === 0) return;
    this.container = container;
    this.count = count;
    this.action = action ? action : () => this.cancel();
    this.predicate = predicate;
    this.bSort = bSort;
    this.indices = [];
    this.index = 0;
    this.bExit = bExit;
    this.title = title;
    this.cancelled = false;

    if (!Carousel.elem) {
      Carousel.elem = document.getElementById('carousel');
      Carousel.elem.children[0].addEventListener('click', () => Carousel.curr.cancel(), false);
    }
    this.elem = Carousel.elem;
    document.getElementsByTagName('main')[0].classList.remove('noclick');

    this.elem.children[0].classList.remove('noclick');
    this.previews = this.elem.getElementsByClassName('card-lg');
    this.desc = this.elem.getElementsByClassName('card-description')[0];
    this.title_elem = this.elem.children[2];
    [...this.elem.children[0].children].forEach((e) =>
      e.addEventListener('mouseout', (evt) => Carousel.curr?.nudge(0)),
    );
    this.elem.children[0].style.setProperty('--carousel-trans-time', '0.25s');
  }

  // Initializes the current Carousel
  start() {
    if (!this.elem) return;
    this.indices = this.container.cards.reduce(
      (a, c, i) => (!this.predicate || this.predicate(c) ? a.concat([i]) : a),
      [],
    );
    if (this.indices.length <= 0) return this.exit();
    if (this.bSort)
      this.indices.sort((a, b) => Card.compare(this.container.cards[a], this.container.cards[b]));

    this.update();
    Carousel.setCurrent(this);

    if (this.title) {
      this.title_elem.innerHTML = this.title;
      this.title_elem.classList.remove('hide');
    } else {
      this.title_elem.classList.add('hide');
    }
    AudioManager.playSFX('open');
    this.elem.classList.remove('hide');
    ui.enablePlayer(true);
  }

  // Called by the client to cycle cards displayed by n
  shift(event, n) {
    (event || window.event).stopPropagation();
    this.index = Math.max(0, Math.min(this.indices.length - 1, this.index + n));
    AudioManager.playSFX('ui_card');
    this.update();
  }

  // called when mousing over/out of one of the carousel cards
  nudge(offset = 0) {
    const parentClasslist = this.elem.children[0].classList;
    parentClasslist.remove('left');
    parentClasslist.remove('right');
    const magnitude = offset === 2 ? -1 * Math.sign(offset) : -0.6 * offset;
    this.elem.children[0].style.setProperty('--magnitude', magnitude);
    if (offset < 0) {
      parentClasslist.add('left');
    } else if (offset > 0) {
      parentClasslist.add('right');
    }
  }

  // Called by client to perform action on the middle card in focus
  async select(event) {
    (event || window.event).stopPropagation();
    --this.count;
    if (this.isLastSelection()) this.elem.classList.add('hide');
    if (this.count <= 0) ui.enablePlayer(false);
    await this.action(this.container, this.indices[this.index]);
    if (this.isLastSelection() && !this.cancelled) return this.exit();
    this.update();
  }

  // Called by client to exit out of the current Carousel if allowed. Enables player interraction.
  cancel() {
    if (this.bExit) {
      this.cancelled = true;
      AudioManager.playSFX('discard');
      this.exit();
    }
    ui.enablePlayer(true);
  }

  // Returns true if there are no more cards to view or select
  isLastSelection() {
    return this.count <= 0 || this.indices.length === 0;
  }

  // Updates the visuals of the current selection of cards
  update() {
    this.indices = this.container.cards.reduce(
      (a, c, i) => (!this.predicate || this.predicate(c) ? a.concat([i]) : a),
      [],
    );
    if (this.indices.length <= 0) {
      return this.exit();
    }
    if (this.index >= this.indices.length) this.index = this.indices.length - 1;
    for (let i = 0; i < this.previews.length; i++) {
      let curr = this.index - 2 + i;
      if (curr >= 0 && curr < this.indices.length) {
        let card = this.container.cards[this.indices[curr]];
        this.previews[i].style.backgroundImage = largeURL(card.faction + '_' + card.filename);
        this.previews[i].classList.remove('hide');
        this.previews[i].classList.remove('noclick');
      } else {
        this.previews[i].style.backgroundImage = '';
        this.previews[i].classList.add('hide');
        this.previews[i].classList.add('noclick');
      }
    }
    ui.setDescription(this.container.cards[this.indices[this.index]], this.desc);
  }

  // Clears and quits the current carousel
  exit() {
    for (let x of this.previews) x.style.backgroundImage = '';
    this.elem.classList.add('hide');
    Carousel.clearCurrent();
    ui.quitCarousel();
  }

  // Statically sets the current carousel
  static setCurrent(curr) {
    this.curr = curr;
  }

  // Statically clears the current carousel
  static clearCurrent() {
    this.curr = null;
  }
}

// Custom confirmation windows
class Popup {
  constructor(yesName, yes, noName, no, header, description, alpha = 0.95) {
    this.yes = yes ? yes : () => {};
    this.no = no ? no : () => {};

    this.elem = document.getElementById('popup');
    let main = this.elem.children[0];
    main.children[0].innerHTML = header ? header : '';
    main.children[1].innerHTML = description ? description : '';
    main.children[2].children[0].innerHTML = yesName ? yesName : 'Yes';
    main.children[2].children[1].innerHTML = noName ? noName : 'No';

    const bgColor = new RGBA(10, 10, 10, alpha);
    this.elem.style.backgroundColor = bgColor.toString();

    this.elem.classList.remove('hide');
    Popup.setCurrent(this);
    ui.enablePlayer(true);
  }

  // Sets this as the current popup window
  static setCurrent(curr) {
    this.curr = curr;
  }

  // Unsets this as the current popup window
  static clearCurrent() {
    this.curr = null;
  }

  // Called when client selects the positive aciton
  selectYes() {
    this.clear();
    this.yes();
    return true;
  }

  // Called when client selects the negative option
  selectNo() {
    this.clear();
    this.no();
    return false;
  }

  // Clears the popup and diables player interraction
  clear() {
    ui.enablePlayer(false);
    this.elem.classList.add('hide');
    Popup.clearCurrent();
  }
}

// Screen used to customize, import and export deck contents
const AudioManager = { init() {}, async playSFX() {}, async play() {} };
const Settings = {
  soundEffects: { isEnabled: () => false },
  notifications: { isEnabled: () => false },
  music: { isEnabled: () => false },
};
class GameEvent {
  constructor(id, signature) {
    this.id = id;
    this.signature = signature;
    if (!signature) {
      throw 'Must pass in a signature as an array of param names';
    }
  }
  bind(listenter) {
    window.addEventListener(this.id, listenter);
  }
  unbind(listenter) {
    window.removeEventListener(this.id, listenter);
  }
  dispatch(...params) {
    const detail = {};
    for (let i = 0; i < params.length && i < this.signature.length; i++) {
      detail[this.signature[i]] = params[i];
    }
    window.dispatchEvent(new CustomEvent(this.id, { detail: detail }));
  }
}

class EventManager {
  static rowSelected;
  static previewCancelled;

  constructor() {
    EventManager.rowSelected = new GameEvent('row-selected', ['row', 'player']);
    EventManager.previewCancelled = new GameEvent('preview-cancelled', []);
    EventManager.gameOpened = new GameEvent('game-opened', []);
    EventManager.customizationOpened = new GameEvent('customize-opened', []);
    EventManager.roundPassed = new GameEvent('round-passed', ['player', 'round']);
    EventManager.roundStarted = new GameEvent('round-started', ['round', 'starting-player']);
    EventManager.roundEnded = new GameEvent('round-started', ['round', 'points-me', 'points-op']);
    EventManager.gameStateChanged = new GameEvent('game-state-changed', ['oldState', 'newState']);
  }
}

// Translates a card between two containers
async function translateTo(card, container_source, container_dest) {
  if (!container_dest || !container_source) return;
  if (container_dest === player_op.hand && container_source === player_op.deck) return;

  let elem = card.elem;
  let source = !container_source
    ? card.elem
    : getSourceElem(card, container_source, container_dest);
  let dest = getDestinationElem(card, container_source, container_dest);
  if (!isInDocument(elem)) source.appendChild(elem);
  let x = trueOffsetLeft(dest) - trueOffsetLeft(elem) + dest.offsetWidth / 2 - elem.offsetWidth;
  let y = trueOffsetTop(dest) - trueOffsetTop(elem) + dest.offsetHeight / 2 - elem.offsetHeight / 2;
  if (container_dest instanceof Row && container_dest.cards.length !== 0 && !card.isSpecial()) {
    x +=
      container_dest.getSortedIndex(card) === container_dest.cards.length
        ? elem.offsetWidth / 2
        : -elem.offsetWidth / 2;
  }
  if (card.holder.controller instanceof ControllerAI) x += elem.offsetWidth / 2;
  if (container_source instanceof Row && container_dest instanceof Grave && !card.isSpecial()) {
    let mid = trueOffset(container_source.elem, true) + container_source.elem.offsetWidth / 2;
    x += trueOffset(elem, true) - mid;
  }
  if (container_source instanceof Row && container_dest === player_me.hand) y *= 7 / 8;
  await translate(elem, x, y);

  // Returns true if the element is visible in the viewport
  function isInDocument(elem) {
    return elem.getBoundingClientRect().width !== 0;
  }

  // Returns the true offset of a nested element in the viewport
  function trueOffset(elem, left) {
    let total = 0;
    let curr = elem;
    while (curr) {
      total += left ? curr.offsetLeft : curr.offsetTop;
      curr = curr.parentElement;
    }
    return total;
  }
  function trueOffsetLeft(elem) {
    return trueOffset(elem, true);
  }
  function trueOffsetTop(elem) {
    return trueOffset(elem, false);
  }

  // Returns the source container's element to transition from
  function getSourceElem(card, source, dest) {
    if (source instanceof HandAI) return source.hidden_elem;
    if (source instanceof Deck) return source.elem.children[source.elem.children.length - 2];
    return source.elem;
  }

  // Returns the destination container's element to transition to
  function getDestinationElem(card, source, dest) {
    if (dest instanceof HandAI) return dest.hidden_elem;
    if (card.isSpecial() && dest instanceof Row) return dest.elem_special;
    if (dest instanceof Row || dest instanceof Hand || dest instanceof Weather) {
      if (dest.cards.length === 0) return dest.elem;
      let index = dest.getSortedIndex(card);
      let dcard = dest.cards[index === dest.cards.length ? index - 1 : index];
      return dcard.elem;
    }
    return dest.elem;
  }
}

// Translates an element by x from the left and y from the top
async function translate(elem, x, y) {
  let vw100 = 100 / document.getElementById('dimensions').offsetWidth;
  x *= vw100;
  y *= vw100;
  elem.style.transform = 'translate(' + x + 'vw, ' + y + 'vw)';
  let margin = elem.style.marginLeft;
  elem.style.marginRight = -elem.offsetWidth * vw100 + 'vw';
  elem.style.marginLeft = '';
  await sleep(499);
  elem.style.transform = '';
  elem.style.position = '';
  elem.style.marginLeft = margin;
  elem.style.marginRight = margin;
}

// Fades out an element until hidden over the duration
async function fadeOut(elem, duration) {
  await fade(false, elem, duration);
}

// Fades in an element until opaque over the duration
async function fadeIn(elem, duration) {
  await fade(true, elem, duration);
}

// Fades an element over a duration
async function fade(fadeIn, elem, dur) {
  if (!elem) return;
  return new Promise((res) => {
    const startingOpacity = toInteger(elem.style.opacity);
    const endOpacity = fadeIn ? 1 : 0;
    const startTime = Date.now();
    const endTime = startTime + dur;
    if (fadeIn) elem.classList.remove('hide');
    const timer = setInterval(() => {
      const currTime = Date.now();
      const op = clamp(
        startingOpacity,
        endOpacity,
        map(startTime, endTime, startingOpacity, endOpacity, currTime),
      );
      elem.style.opacity = op;
      if (op === endOpacity) {
        clearInterval(timer);
        if (!fadeIn) elem.classList.add('hide');
        res();
      }
    }, DUR_FADE_STEP);
  });
}

//      Get Image paths
function iconURL(name, ext = 'png') {
  return imgURL('icons/' + name, ext);
}
function largeURL(name, ext = 'jpg') {
  return imgURL('lg/' + name, ext);
}
function smallURL(name, ext = 'jpg') {
  return imgURL('sm/' + name, ext);
}
function imgURL(path, ext) {
  return "url('img/" + path + '.' + ext + "')";
}

// get sound effect path
function audioURL(name, ext = 'mp3') {
  if (typeof name === 'string' || name instanceof String) {
    if (name.includes('.')) {
      return 'sfx/' + name;
    }
  } else if (name['name']) {
    if (name['ext']) {
      ext = name['ext'];
    }
    name = name['name'];
  }
  return 'sfx/' + name + '.' + ext;
}

// Get audio instance
function getAudio(name, ext = 'mp3') {
  return new Audio(audioURL(name, ext));
}
// Play sound effect
async function playAudio(name, ext = 'mp3') {
  return await asyncAudio(getAudio(name, ext));
}

function asyncAudio(audio) {
  if (!userInteracted || !audio) return;
  return new Promise((r) => {
    audio.play();
    audio.onended = r;
  });
}

// Pauses execution until the passed number of milliseconds as expired
function sleep(ms) {
  return new Promise((resolve) => setTimeout(resolve, ms));
  //return new Promise(resolve => setTimeout(() => {if (func) func(); return resolve();}, ms));
}

// Suspends execution until the predicate condition is met, checking every ms milliseconds
function sleepUntil(predicate, ms) {
  return new Promise((resolve) => {
    let timer = setInterval(function () {
      if (predicate()) {
        clearInterval(timer);
        resolve();
      }
    }, ms);
  });
}

// Initializes the interractive YouTube object

/*----------------------------------------------------*/

const eventManager = new EventManager();
let userInteracted = false;
var ui = new UI();
var board = new Board();
var weather = new Weather();
var game = new Game();
var player_me, player_op;
AudioManager.init();

ui.enablePlayer(false);
// Arena supplies its own deck selector; classic.html retains the upstream UI.
let dm = null;

document.addEventListener('click', () => (userInteracted = true), { once: true });
