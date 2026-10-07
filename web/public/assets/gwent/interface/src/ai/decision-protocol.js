// Plain JSON across the browser/server boundary; no live engine objects or code.
export function validateDecision(result, actions) {
  if (
    !Number.isInteger(result?.actionIndex) ||
    result.actionIndex < 0 ||
    result.actionIndex >= actions.length
  )
    throw Error('LLM 返回了非法行动');
  return {
    actionIndex: result.actionIndex,
    reason: typeof result.reason === 'string' ? result.reason.slice(0, 160) : '',
  };
}

export function validateSnapshot(snapshot) {
  if (
    !snapshot ||
    !Number.isInteger(snapshot.round) ||
    snapshot.round < 1 ||
    snapshot.round > 3 ||
    !Array.isArray(snapshot.legalActions) ||
    !snapshot.legalActions.length ||
    snapshot.legalActions.length > 200 ||
    !Array.isArray(snapshot.hand) ||
    snapshot.hand.length > 100 ||
    !Array.isArray(snapshot.board) ||
    snapshot.board.length !== 6
  )
    throw Error('无效的对局快照');
  for (const [index, action] of snapshot.legalActions.entries()) {
    if (
      action.index !== index ||
      !['play_card', 'pass', 'leader'].includes(action.type) ||
      (action.type === 'play_card' &&
        (typeof action.cardId !== 'string' ||
          !snapshot.hand.some((card) => card.id === action.cardId))) ||
      (action.row !== undefined && !['close', 'ranged', 'siege', 'weather'].includes(action.row))
    )
      throw Error('无效的合法行动列表');
  }
  const text = (value, limit = 160) => (typeof value === 'string' ? value.slice(0, limit) : '');
  const number = (value) => (Number.isFinite(value) ? value : 0);
  const form = (value) => ({
    id: text(value?.id),
    name: text(value?.name),
    power: number(value?.power),
    row: text(value?.row),
    abilities: Array.isArray(value?.abilities)
      ? value.abilities.slice(0, 8).map((id) => text(id))
      : [],
  });
  const card = (value) => ({
    id: text(value?.id),
    name: text(value?.name),
    power: number(value?.power),
    basePower: number(value?.basePower),
    hero: !!value?.hero,
    row: text(value?.row),
    ...(value?.musterGroup ? { musterGroup: text(value.musterGroup, 80) } : {}),
    ...(value?.transformForm ? { transformForm: form(value.transformForm) } : {}),
    ...(value?.avengerForm ? { avengerForm: form(value.avengerForm) } : {}),
    ...(value?.generated ? { generated: true, ephemeral: !!value.ephemeral } : {}),
    abilities: Array.isArray(value?.abilities)
      ? value.abilities.slice(0, 8).map((ability) => ({
          id: text(ability.id),
          description: text(ability.description, 500),
        }))
      : [],
  });
  const owner = (value) => ({
    faction: text(value?.faction),
    ...(value?.passive
      ? {
          passive: {
            name: text(value.passive.name),
            description: text(value.passive.description, 500),
          },
        }
      : {}),
    lives: number(value?.lives),
    passed: !!value?.passed,
    handCount: number(value?.handCount),
    deckCount: number(value?.deckCount),
    boost: number(value?.boost),
    shield: number(value?.shield),
    leader: { ...card(value?.leader), available: !!value?.leader?.available },
    grave: Array.isArray(value?.grave) ? value.grave.slice(0, 100).map(card) : [],
  });
  return {
    round: snapshot.round,
    playerPower: number(snapshot.playerPower),
    enemyPower: number(snapshot.enemyPower),
    enemyHandCount: number(snapshot.enemyHandCount),
    self: {
      ...owner(snapshot.self),
      knownDeckTop: Array.isArray(snapshot.self?.knownDeckTop)
        ? snapshot.self.knownDeckTop.slice(0, 2).map(card)
        : [],
    },
    enemy: owner(snapshot.enemy),
    board: snapshot.board.map((row) => {
      if (
        !row ||
        !['close', 'ranged', 'siege'].includes(row.row) ||
        !['self', 'enemy'].includes(row.side) ||
        !Array.isArray(row.cards) ||
        row.cards.length > 100
      )
        throw Error('无效的战线快照');
      return {
        row: row.row,
        side: row.side,
        weather: !!row.weather,
        effects: {
          horn: number(row.effects?.horn),
          morale: number(row.effects?.morale),
          catalyst: number(row.effects?.catalyst),
        },
        cards: row.cards.map(card),
      };
    }),
    hand: snapshot.hand.map(card),
    legalActions: snapshot.legalActions.map((action) => ({
      index: action.index,
      type: action.type,
      ...(action.type === 'play_card'
        ? {
            cardId: text(action.cardId),
            ...(action.row ? { row: action.row } : {}),
          }
        : {}),
    })),
  };
}
