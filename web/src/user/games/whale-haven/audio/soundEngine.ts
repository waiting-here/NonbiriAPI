/**
 * 鲸鱼湾小筑 · 零依赖合成萌系音效与深海白噪音引擎 (Web Audio API)
 *
 * 设计特点：
 * 1. 纯合成 Q 弹水滴、气泡、风铃与硬币声，无需加载任何外部音频资源，零延迟瞬发。
 * 2. 自动处理浏览器 Autoplay 限制：在首次指针/键盘操作时惰性唤醒 AudioContext。
 * 3. 可选开启「深海潜艇白噪音」：柔和低频暗流 + 随机深海微气泡声。
 */

export type SoundEffectId =
  | 'headpat' // 摸摸头：软糯双气泡 + 温暖泛音
  | 'poke' // 戳肚皮：Q弹果冻水滴音
  | 'angry_pout' // 连戳生气鼓腮：气鼓鼓低音噗噜
  | 'feed_rice' // 添饭盛米：陶瓷青花碗轻叩 + 暖音上扬
  | 'eat_teacake' // 炫下午茶仙贝：清脆两口 + 幸福高音铃
  | 'coin_harvest' // 收小鲸币：清脆叮当连响泛音
  | 'catch_alert' // 点开抓包客鲸弹窗：呆萌惊叹小滑音
  | 'catch_flick' // 弹脑瓜崩：清脆“啪嗒”弹指 + 委屈下滑音
  | 'catch_gentle' // 温和请回：礼貌柔和木琴双音
  | 'catch_treat' // 热情款待：欢快三和弦水铃
  | 'catch_hire' // 雇佣穿制服上岗：元气打工打卡号角
  | 'sleep' // 钻进蓝鲸软窝睡觉：舒缓摇篮曲下行泛音
  | 'wake' // 海螺铃轻拍唤醒：清晨海螺风铃上行
  | 'chat_bubble' // 唠嗑/气泡弹出：轻巧“啵”一声小水泡
  | 'ui_click' // 抽屉/标签切换：极轻柔水滴微触感
  | 'error_soft'; // 积分/口粮不足：柔和低音提示

export interface AudioSettings {
  sfxEnabled: boolean;
  ambientEnabled: boolean;
  volume: number; // 0..1
}

const AUDIO_SETTINGS_KEY = 'nonbiri_whale_audio_v1';

const DEFAULT_SETTINGS: AudioSettings = {
  sfxEnabled: true,
  ambientEnabled: false,
  volume: 0.65,
};

export function loadAudioSettings(): AudioSettings {
  try {
    const raw = window.localStorage.getItem(AUDIO_SETTINGS_KEY);
    if (!raw) return { ...DEFAULT_SETTINGS };
    const parsed = JSON.parse(raw) as Partial<AudioSettings>;
    return {
      sfxEnabled:
        typeof parsed.sfxEnabled === 'boolean' ? parsed.sfxEnabled : DEFAULT_SETTINGS.sfxEnabled,
      ambientEnabled:
        typeof parsed.ambientEnabled === 'boolean'
          ? parsed.ambientEnabled
          : DEFAULT_SETTINGS.ambientEnabled,
      volume:
        typeof parsed.volume === 'number' && Number.isFinite(parsed.volume)
          ? Math.max(0, Math.min(1, parsed.volume))
          : DEFAULT_SETTINGS.volume,
    };
  } catch {
    return { ...DEFAULT_SETTINGS };
  }
}

export function saveAudioSettings(settings: AudioSettings): void {
  try {
    window.localStorage.setItem(AUDIO_SETTINGS_KEY, JSON.stringify(settings));
  } catch {
    // ignore storage quota
  }
}

class WhaleSoundEngine {
  private ctx: AudioContext | null = null;
  private masterGain: GainNode | null = null;
  private settings: AudioSettings =
    typeof window !== 'undefined' ? loadAudioSettings() : { ...DEFAULT_SETTINGS };

  // Ambient nodes
  private ambientStopFn: (() => void) | null = null;
  private ambientBubbleTimer: number | null = null;
  mount() {
    const unlock = () => {
      if (this.ctx && this.ctx.state === 'suspended') {
        this.ctx.resume().catch(() => {});
      }
      if (this.settings.ambientEnabled && !this.ambientStopFn) {
        this.startAmbient();
      }
    };
    window.addEventListener('pointerdown', unlock, { passive: true });
    window.addEventListener('keydown', unlock, { passive: true });
    return () => {
      window.removeEventListener('pointerdown', unlock);
      window.removeEventListener('keydown', unlock);
      this.stopAmbient();
      void this.ctx?.close().catch(() => undefined);
      this.ctx = null;
      this.masterGain = null;
    };
  }

  private ensureContext(): AudioContext | null {
    if (typeof window === 'undefined') return null;
    if (!this.ctx) {
      const AudioCtx =
        window.AudioContext ||
        (window as unknown as { webkitAudioContext?: typeof AudioContext }).webkitAudioContext;
      if (!AudioCtx) return null;
      this.ctx = new AudioCtx();
      this.masterGain = this.ctx.createGain();
      this.masterGain.gain.value = this.settings.volume;
      this.masterGain.connect(this.ctx.destination);
    }
    if (this.ctx.state === 'suspended') {
      this.ctx.resume().catch(() => {});
    }
    return this.ctx;
  }

  getSettings(): AudioSettings {
    return { ...this.settings };
  }

  updateSettings(patch: Partial<AudioSettings>): AudioSettings {
    this.settings = {
      ...this.settings,
      ...patch,
      volume:
        patch.volume !== undefined ? Math.max(0, Math.min(1, patch.volume)) : this.settings.volume,
    };
    saveAudioSettings(this.settings);

    if (this.masterGain && this.ctx) {
      this.masterGain.gain.setTargetAtTime(this.settings.volume, this.ctx.currentTime, 0.03);
    }

    if (this.settings.ambientEnabled) {
      this.startAmbient();
    } else {
      this.stopAmbient();
    }

    return this.getSettings();
  }

  /**
   * 基础合成原语：带频率包络与柔和 ADSR 的单音
   */
  private tone(opts: {
    type?: OscillatorType;
    startFreq: number;
    endFreq?: number;
    delay?: number;
    duration: number;
    gain: number;
    attack?: number;
  }) {
    const ctx = this.ensureContext();
    if (!ctx || !this.masterGain) return;

    const now = ctx.currentTime + (opts.delay ?? 0);
    const osc = ctx.createOscillator();
    const g = ctx.createGain();

    osc.type = opts.type ?? 'sine';
    osc.frequency.setValueAtTime(Math.max(20, opts.startFreq), now);
    if (opts.endFreq !== undefined && opts.endFreq !== opts.startFreq) {
      osc.frequency.exponentialRampToValueAtTime(Math.max(20, opts.endFreq), now + opts.duration);
    }

    const attack = Math.min(opts.attack ?? 0.01, opts.duration * 0.45);
    g.gain.setValueAtTime(0.0001, now);
    g.gain.linearRampToValueAtTime(opts.gain, now + attack);
    g.gain.exponentialRampToValueAtTime(0.0001, now + opts.duration);

    osc.connect(g);
    g.connect(this.masterGain);

    osc.start(now);
    osc.stop(now + opts.duration + 0.015);
  }

  /**
   * 短促带通噪声（用于弹脑瓜崩脆响、盛米颗粒感、吃仙贝咔嚓声）
   */
  private noiseBurst(opts: {
    delay?: number;
    duration: number;
    bandFreq: number;
    q?: number;
    gain: number;
  }) {
    const ctx = this.ensureContext();
    if (!ctx || !this.masterGain) return;

    const sampleCount = Math.max(128, Math.floor(ctx.sampleRate * opts.duration));
    const buffer = ctx.createBuffer(1, sampleCount, ctx.sampleRate);
    const data = buffer.getChannelData(0);
    for (let i = 0; i < sampleCount; i++) {
      data[i] = Math.random() * 2 - 1;
    }

    const now = ctx.currentTime + (opts.delay ?? 0);
    const src = ctx.createBufferSource();
    src.buffer = buffer;

    const filter = ctx.createBiquadFilter();
    filter.type = 'bandpass';
    filter.frequency.setValueAtTime(opts.bandFreq, now);
    filter.Q.setValueAtTime(opts.q ?? 2.4, now);

    const g = ctx.createGain();
    g.gain.setValueAtTime(opts.gain, now);
    g.gain.exponentialRampToValueAtTime(0.0001, now + opts.duration);

    src.connect(filter);
    filter.connect(g);
    g.connect(this.masterGain);

    src.start(now);
    src.stop(now + opts.duration + 0.01);
  }

  /**
   * 播放指定萌系交互音效
   */
  play(id: SoundEffectId, options?: { coinCount?: number }) {
    if (!this.settings.sfxEnabled || this.settings.volume <= 0.001) return;

    switch (id) {
      case 'headpat': {
        // 软糯水滴气泡双连音 + 甜蜜高八度泛音
        this.tone({ type: 'sine', startFreq: 420, endFreq: 690, duration: 0.11, gain: 0.22 });
        this.tone({
          type: 'sine',
          startFreq: 560,
          endFreq: 920,
          delay: 0.085,
          duration: 0.14,
          gain: 0.24,
        });
        this.tone({
          type: 'triangle',
          startFreq: 1046.5,
          endFreq: 1318.5,
          delay: 0.15,
          duration: 0.18,
          gain: 0.09,
        });
        break;
      }

      case 'poke': {
        // Q 弹果冻肚皮回弹声 (boing-bloop!)
        this.tone({ type: 'sine', startFreq: 260, endFreq: 590, duration: 0.08, gain: 0.28 });
        this.tone({
          type: 'triangle',
          startFreq: 540,
          endFreq: 340,
          delay: 0.065,
          duration: 0.11,
          gain: 0.18,
        });
        break;
      }

      case 'angry_pout': {
        // 鼓腮抗议：气鼓鼓双低音噗噜
        this.tone({ type: 'triangle', startFreq: 330, endFreq: 210, duration: 0.13, gain: 0.26 });
        this.tone({
          type: 'triangle',
          startFreq: 280,
          endFreq: 165,
          delay: 0.12,
          duration: 0.18,
          gain: 0.28,
        });
        break;
      }

      case 'feed_rice': {
        // 青花瓷碗轻叩 + 热气腾腾五声音阶上行
        this.noiseBurst({ duration: 0.06, bandFreq: 1650, q: 4.5, gain: 0.14 });
        const notes = [523.25, 659.25, 783.99, 1046.5];
        notes.forEach((f, idx) => {
          this.tone({
            type: 'sine',
            startFreq: f,
            endFreq: f * 1.02,
            delay: 0.04 + idx * 0.055,
            duration: 0.16,
            gain: 0.18,
          });
        });
        break;
      }

      case 'eat_teacake': {
        // 咔嚓两口海苔仙贝 + 满足星星铃
        this.noiseBurst({ duration: 0.045, bandFreq: 2400, q: 2.2, gain: 0.18 });
        this.noiseBurst({ delay: 0.09, duration: 0.045, bandFreq: 2700, q: 2.2, gain: 0.18 });
        this.tone({
          type: 'sine',
          startFreq: 784,
          endFreq: 1174.6,
          delay: 0.17,
          duration: 0.2,
          gain: 0.2,
        });
        this.tone({ type: 'triangle', startFreq: 1568, delay: 0.24, duration: 0.24, gain: 0.12 });
        break;
      }

      case 'coin_harvest': {
        // 收取小鲸币：根据收获数量连响 2~5 声清脆金币水铃
        const chimes = Math.max(2, Math.min(5, options?.coinCount ?? 3));
        const scale = [1046.5, 1318.5, 1567.98, 1760, 2093];
        for (let i = 0; i < chimes; i++) {
          const f = scale[i % scale.length];
          this.tone({
            type: 'sine',
            startFreq: f,
            endFreq: f * 1.015,
            delay: i * 0.065,
            duration: 0.24,
            gain: 0.22,
            attack: 0.004,
          });
          this.tone({
            type: 'triangle',
            startFreq: f * 2,
            delay: i * 0.065,
            duration: 0.11,
            gain: 0.07,
            attack: 0.003,
          });
        }
        break;
      }

      case 'catch_alert': {
        // 抓包客鲸：呆萌小问号上挑音
        this.tone({ type: 'sine', startFreq: 480, endFreq: 760, duration: 0.09, gain: 0.2 });
        this.tone({
          type: 'sine',
          startFreq: 760,
          endFreq: 980,
          delay: 0.08,
          duration: 0.11,
          gain: 0.18,
        });
        break;
      }

      case 'catch_flick': {
        // 弹脑瓜崩：清脆弹指“啪嗒” + 眼泪汪汪委屈滑音 + 掉两枚小鲸币
        this.noiseBurst({ duration: 0.03, bandFreq: 3200, q: 3.6, gain: 0.28 });
        this.tone({
          type: 'sine',
          startFreq: 920,
          endFreq: 360,
          delay: 0.02,
          duration: 0.18,
          gain: 0.24,
        });
        // 掉下两枚小鲸币叮当
        this.tone({ type: 'sine', startFreq: 1318.5, delay: 0.18, duration: 0.16, gain: 0.16 });
        this.tone({ type: 'sine', startFreq: 1568, delay: 0.25, duration: 0.2, gain: 0.18 });
        break;
      }

      case 'catch_gentle': {
        // 温和请回：礼貌柔和双音
        this.tone({ type: 'sine', startFreq: 659.25, duration: 0.14, gain: 0.18 });
        this.tone({ type: 'sine', startFreq: 523.25, delay: 0.11, duration: 0.2, gain: 0.16 });
        break;
      }

      case 'catch_treat': {
        // 热情款待：温暖大三和弦琶音
        [523.25, 659.25, 783.99, 1046.5].forEach((f, idx) => {
          this.tone({
            type: 'triangle',
            startFreq: f,
            delay: idx * 0.06,
            duration: 0.24,
            gain: 0.18,
          });
        });
        break;
      }

      case 'catch_hire': {
        // 雇佣上岗：精神抖擞的打工打卡双拍号角
        this.tone({ type: 'triangle', startFreq: 587.33, duration: 0.09, gain: 0.2 });
        this.tone({ type: 'triangle', startFreq: 587.33, delay: 0.1, duration: 0.09, gain: 0.2 });
        this.tone({ type: 'triangle', startFreq: 880, delay: 0.2, duration: 0.26, gain: 0.24 });
        this.tone({ type: 'sine', startFreq: 1174.66, delay: 0.22, duration: 0.26, gain: 0.14 });
        break;
      }

      case 'sleep': {
        // 哄睡：轻柔摇篮曲下行泛音
        [783.99, 659.25, 523.25, 392.0].forEach((f, idx) => {
          this.tone({
            type: 'sine',
            startFreq: f,
            delay: idx * 0.11,
            duration: 0.34,
            gain: 0.14,
            attack: 0.03,
          });
        });
        break;
      }

      case 'wake': {
        // 唤醒：清晨海螺风铃轻快上行
        [523.25, 659.25, 783.99, 1046.5].forEach((f, idx) => {
          this.tone({
            type: 'sine',
            startFreq: f,
            delay: idx * 0.075,
            duration: 0.25,
            gain: 0.16,
          });
        });
        break;
      }

      case 'chat_bubble': {
        // 气泡啵一声 (cute water pop)
        const jitter = 0.94 + Math.random() * 0.14;
        this.tone({
          type: 'sine',
          startFreq: 370 * jitter,
          endFreq: 740 * jitter,
          duration: 0.085,
          gain: 0.19,
          attack: 0.005,
        });
        break;
      }

      case 'ui_click': {
        // 轻巧微水滴触感音
        this.tone({
          type: 'sine',
          startFreq: 520,
          endFreq: 680,
          duration: 0.045,
          gain: 0.1,
          attack: 0.003,
        });
        break;
      }

      case 'error_soft': {
        this.tone({ type: 'sine', startFreq: 300, endFreq: 220, duration: 0.14, gain: 0.16 });
        break;
      }
    }
  }

  /**
   * 开启轻柔「深海潜艇暗流 + 微气泡」环境白噪音
   */
  private startAmbient() {
    if (this.ambientStopFn || !this.settings.ambientEnabled) return;
    const ctx = this.ensureContext();
    if (!ctx || !this.masterGain) return;

    // 生成 4 秒无缝柔和粉红/深海低频噪声缓冲
    const seconds = 4;
    const length = ctx.sampleRate * seconds;
    const buffer = ctx.createBuffer(1, length, ctx.sampleRate);
    const channel = buffer.getChannelData(0);
    let b0 = 0;
    let b1 = 0;
    let b2 = 0;
    for (let i = 0; i < length; i++) {
      const white = Math.random() * 2 - 1;
      b0 = 0.99765 * b0 + white * 0.099046;
      b1 = 0.963 * b1 + white * 0.2965164;
      b2 = 0.57 * b2 + white * 1.0526913;
      const pink = (b0 + b1 + b2 + white * 0.1848) * 0.08;
      // 首尾平滑淡入淡出避免循环接缝咔哒声
      const edgeSamples = Math.floor(ctx.sampleRate * 0.08);
      let env = 1;
      if (i < edgeSamples) env = i / edgeSamples;
      else if (i > length - edgeSamples) env = (length - i) / edgeSamples;
      channel[i] = pink * env;
    }

    const noiseSrc = ctx.createBufferSource();
    noiseSrc.buffer = buffer;
    noiseSrc.loop = true;

    // 低通滤波模拟深海木屋窗外的温柔海流声，并用慢速 LFO 模拟潮汐呼吸起伏
    const lowpass = ctx.createBiquadFilter();
    lowpass.type = 'lowpass';
    lowpass.frequency.value = 290;
    lowpass.Q.value = 0.7;

    const lfo = ctx.createOscillator();
    const lfoGain = ctx.createGain();
    lfo.type = 'sine';
    lfo.frequency.value = 0.14; // 约 7 秒一次温柔潮汐起伏
    lfoGain.gain.value = 110;
    lfo.connect(lfoGain);
    lfoGain.connect(lowpass.frequency);

    const ambGain = ctx.createGain();
    ambGain.gain.setValueAtTime(0.0001, ctx.currentTime);
    ambGain.gain.linearRampToValueAtTime(0.24, ctx.currentTime + 1.2);

    noiseSrc.connect(lowpass);
    lowpass.connect(ambGain);
    ambGain.connect(this.masterGain);

    noiseSrc.start();
    lfo.start();

    // 每隔 3.2~7.5 秒在海景窗外轻轻冒一两颗微弱气泡音
    const scheduleGentleBubble = () => {
      if (!this.settings.ambientEnabled) return;
      const delayMs = 3200 + Math.random() * 4300;
      this.ambientBubbleTimer = window.setTimeout(() => {
        if (this.settings.ambientEnabled && this.ctx?.state === 'running') {
          const base = 320 + Math.random() * 240;
          this.tone({
            type: 'sine',
            startFreq: base,
            endFreq: base * 1.65,
            duration: 0.09,
            gain: 0.045,
            attack: 0.01,
          });
        }
        scheduleGentleBubble();
      }, delayMs);
    };
    scheduleGentleBubble();

    this.ambientStopFn = () => {
      if (this.ambientBubbleTimer) {
        window.clearTimeout(this.ambientBubbleTimer);
        this.ambientBubbleTimer = null;
      }
      try {
        const stopAt = ctx.currentTime + 0.35;
        ambGain.gain.setTargetAtTime(0.0001, ctx.currentTime, 0.08);
        noiseSrc.stop(stopAt);
        lfo.stop(stopAt);
      } catch {
        // ignore already stopped
      }
    };
  }

  private stopAmbient() {
    if (this.ambientStopFn) {
      const stop = this.ambientStopFn;
      this.ambientStopFn = null;
      stop();
    }
  }
}

export const soundEngine = new WhaleSoundEngine();
