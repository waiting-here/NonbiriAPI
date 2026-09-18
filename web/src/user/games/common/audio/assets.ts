export const musicTracks = [
  'lobby',
  'battle',
  'accelerated',
  'danger',
  'win',
  'draw',
  'loss-stinger',
] as const;
export type MusicTrack = (typeof musicTracks)[number];
export type MusicLoop = Exclude<MusicTrack, 'loss-stinger'>;
export type MusicScene = MusicLoop | 'loss';
export type MusicQuality = 'light' | 'lossless';
export type AudioGame = 'likes' | 'bidding' | 'blackjack';

export const musicUrls = {
  light: {
    lobby: new URL('../../../../shared/assets/game-audio/music/lobby.mp3', import.meta.url).href,
    battle: new URL('../../../../shared/assets/game-audio/music/battle.mp3', import.meta.url).href,
    accelerated: new URL(
      '../../../../shared/assets/game-audio/music/accelerated.mp3',
      import.meta.url,
    ).href,
    danger: new URL('../../../../shared/assets/game-audio/music/danger.mp3', import.meta.url).href,
    win: new URL('../../../../shared/assets/game-audio/music/win.mp3', import.meta.url).href,
    draw: new URL('../../../../shared/assets/game-audio/music/draw.mp3', import.meta.url).href,
    'loss-stinger': new URL(
      '../../../../shared/assets/game-audio/music/loss-stinger.mp3',
      import.meta.url,
    ).href,
  },
  lossless: {
    lobby: new URL('../../../../shared/assets/game-audio/music/lobby.flac', import.meta.url).href,
    battle: new URL('../../../../shared/assets/game-audio/music/battle.flac', import.meta.url).href,
    accelerated: new URL(
      '../../../../shared/assets/game-audio/music/accelerated.flac',
      import.meta.url,
    ).href,
    danger: new URL('../../../../shared/assets/game-audio/music/danger.flac', import.meta.url).href,
    win: new URL('../../../../shared/assets/game-audio/music/win.flac', import.meta.url).href,
    draw: new URL('../../../../shared/assets/game-audio/music/draw.flac', import.meta.url).href,
    'loss-stinger': new URL(
      '../../../../shared/assets/game-audio/music/loss-stinger.flac',
      import.meta.url,
    ).href,
  },
} as const;

export const effectUrls = {
  common_select: new URL(
    '../../../../shared/assets/game-audio/sfx/common_select.wav',
    import.meta.url,
  ).href,
  common_confirm: new URL(
    '../../../../shared/assets/game-audio/sfx/common_confirm.wav',
    import.meta.url,
  ).href,
  common_lock: new URL('../../../../shared/assets/game-audio/sfx/common_lock.wav', import.meta.url)
    .href,
  common_match: new URL(
    '../../../../shared/assets/game-audio/sfx/common_match.wav',
    import.meta.url,
  ).href,
  common_win: new URL('../../../../shared/assets/game-audio/sfx/common_win.wav', import.meta.url)
    .href,
  common_loss: new URL('../../../../shared/assets/game-audio/sfx/common_loss.wav', import.meta.url)
    .href,
  common_draw: new URL('../../../../shared/assets/game-audio/sfx/common_draw.wav', import.meta.url)
    .href,
  likes_cast: new URL('../../../../shared/assets/game-audio/sfx/likes_cast.wav', import.meta.url)
    .href,
  likes_score_burst: new URL(
    '../../../../shared/assets/game-audio/sfx/likes_score_burst.wav',
    import.meta.url,
  ).href,
  likes_combo: new URL('../../../../shared/assets/game-audio/sfx/likes_combo.wav', import.meta.url)
    .href,
  likes_pay: new URL('../../../../shared/assets/game-audio/sfx/likes_pay.wav', import.meta.url)
    .href,
  likes_charge: new URL(
    '../../../../shared/assets/game-audio/sfx/likes_charge.wav',
    import.meta.url,
  ).href,
  likes_subscription_refill: new URL(
    '../../../../shared/assets/game-audio/sfx/likes_subscription_refill.wav',
    import.meta.url,
  ).href,
  likes_subscription_upgrade: new URL(
    '../../../../shared/assets/game-audio/sfx/likes_subscription_upgrade.wav',
    import.meta.url,
  ).href,
  likes_buff: new URL('../../../../shared/assets/game-audio/sfx/likes_buff.wav', import.meta.url)
    .href,
  likes_cleanse: new URL(
    '../../../../shared/assets/game-audio/sfx/likes_cleanse.wav',
    import.meta.url,
  ).href,
  likes_stun: new URL('../../../../shared/assets/game-audio/sfx/likes_stun.wav', import.meta.url)
    .href,
  likes_overload: new URL(
    '../../../../shared/assets/game-audio/sfx/likes_overload.wav',
    import.meta.url,
  ).href,
  bidding_reveal: new URL(
    '../../../../shared/assets/game-audio/sfx/bidding_reveal.wav',
    import.meta.url,
  ).href,
  bidding_king: new URL(
    '../../../../shared/assets/game-audio/sfx/bidding_king.wav',
    import.meta.url,
  ).href,
  bidding_pot_add: new URL(
    '../../../../shared/assets/game-audio/sfx/bidding_pot_add.wav',
    import.meta.url,
  ).href,
  bidding_pot_collect: new URL(
    '../../../../shared/assets/game-audio/sfx/bidding_pot_collect.wav',
    import.meta.url,
  ).href,
  blackjack_deal: new URL(
    '../../../../shared/assets/game-audio/sfx/blackjack_deal.wav',
    import.meta.url,
  ).href,
  blackjack_flip: new URL(
    '../../../../shared/assets/game-audio/sfx/blackjack_flip.wav',
    import.meta.url,
  ).href,
  blackjack_double: new URL(
    '../../../../shared/assets/game-audio/sfx/blackjack_double.wav',
    import.meta.url,
  ).href,
  blackjack_split: new URL(
    '../../../../shared/assets/game-audio/sfx/blackjack_split.wav',
    import.meta.url,
  ).href,
  blackjack_natural: new URL(
    '../../../../shared/assets/game-audio/sfx/blackjack_natural.wav',
    import.meta.url,
  ).href,
  blackjack_bust: new URL(
    '../../../../shared/assets/game-audio/sfx/blackjack_bust.wav',
    import.meta.url,
  ).href,
  likes_loss_stinger: new URL(
    '../../../../shared/assets/game-audio/sfx/likes_loss_stinger.wav',
    import.meta.url,
  ).href,
} as const;
export type EffectCue = keyof typeof effectUrls;
export function isEffectCue(cue: string): cue is EffectCue {
  return Object.hasOwn(effectUrls, cue);
}
