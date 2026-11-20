import { useState } from 'react';
import { Dices, Sparkles } from './Icons';
import { soundEngine } from '../audio/soundEngine';
import {
  DEFAULT_WHALE_NAME,
  WHALE_LORE_PROFILE,
  getSpriteUrl,
  pickRandomCuteWhaleName,
} from '../game/catalog';
import { NoticeHeading } from './UiMotifs';

interface WhaleNamingModalProps {
  initialName?: string;
  busy: boolean;
  onConfirmName: (name: string, open: boolean) => void;
}

export function WhaleNamingModal({
  initialName = DEFAULT_WHALE_NAME,
  onConfirmName,
  busy,
}: WhaleNamingModalProps) {
  const [name, setName] = useState(initialName || DEFAULT_WHALE_NAME);
  const [open, setOpen] = useState(true);
  const [diceSpin, setDiceSpin] = useState(0);

  const handleRollCuteName = () => {
    soundEngine.play('ui_click');
    setDiceSpin((n) => n + 1);
    setName(pickRandomCuteWhaleName(name));
  };

  const handleSubmit = (e: React.FormEvent) => {
    e.preventDefault();
    onConfirmName(name.trim() || DEFAULT_WHALE_NAME, open);
  };

  return (
    <div className="whale-adoption">
      <form
        className="modal-panel"

        aria-labelledby="whale-naming-title"
        onSubmit={handleSubmit}
        style={{ maxWidth: 480, gap: '0.85rem' }}
      >
        <NoticeHeading id="whale-naming-title" title="为鲸鱼娘取个名字" />

        <div className="modal-hero-box">
          <img src={getSpriteUrl('cheerful')} alt="鲸鱼娘" className="modal-hero-box__img" />
          <div style={{ fontSize: '0.82rem', lineHeight: 1.5, minWidth: 0 }}>
            <div style={{ fontWeight: 700, marginBottom: '0.25rem' }}>
              「你好呀人类！以后你就是我的长期饲养员啦～」
            </div>
            <div style={{ color: 'var(--nb-color-text-muted)', fontSize: '0.78rem' }}>
              💡 喜好：<strong>热腾腾的白米饭</strong>、<strong>Token 口粮</strong>、
              <strong>趴着摸鱼</strong>
              （随时可在左上角状态卡片修改名字哦～）
            </div>
          </div>
        </div>

        <div style={{ display: 'flex', flexDirection: 'column', gap: '0.45rem' }}>
          <label
            htmlFor="whale-init-name"
            style={{ fontSize: '0.82rem', fontWeight: 700, color: 'var(--nb-color-text)' }}
          >
            她的专属昵称（最多 10 个字，点右侧骰子可随机可爱名字）：
          </label>
          <div style={{ display: 'flex', alignItems: 'stretch', gap: '0.45rem' }}>
            <input
              id="whale-init-name"
              className="kit-input"
              style={{ flex: 1, minWidth: 0 }}
              value={name}
              onChange={(e) => setName(e.target.value)}
              placeholder={DEFAULT_WHALE_NAME}
              maxLength={10}
              autoComplete="off"
            />
            <button
              type="button"
              className="nb-btn"
              style={{
                flexShrink: 0,
                paddingInline: '0.78rem',
                gap: '0.35rem',
                whiteSpace: 'nowrap',
              }}
              onClick={handleRollCuteName}
              title="随机摇一个软萌可爱的名字"
            >
              <Dices
                size={16}
                style={{
                  transition: 'transform 0.28s cubic-bezier(0.34, 1.56, 0.64, 1)',
                  transform: `rotate(${diceSpin * 180}deg)`,
                }}
              />
              随机取名
            </button>
          </div>
        </div>

        <div
          style={{
            fontSize: '0.73rem',
            color: 'var(--nb-color-text-subtle)',
            borderTop: '1px solid var(--nb-color-border)',
            paddingTop: '0.55rem',
            lineHeight: 1.45,
          }}
        >
          {WHALE_LORE_PROFILE.credits}
        </div>

        <label className="wh-choice">
          <input
            type="checkbox"
            checked={open}
            onChange={(event) => setOpen(event.target.checked)}
          />{' '}
          开放小筑，允许互相串门
        </label>
        <small>仅开放的小筑可以互访。领养后可随时在「串门」更改。</small>
        <div className="notice-actions">
          <button disabled={busy} type="submit" className="nb-btn nb-btn--primary">
            <Sparkles size={15} /> 确认叫「{name.trim() || DEFAULT_WHALE_NAME}」，领养回家
          </button>
        </div>
      </form>
    </div>
  );
}
