import { useWhaleText } from '../copy';
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
  const t = useWhaleText();
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
        <NoticeHeading id="whale-naming-title" title={t('为鲸鱼娘取个名字', 'Name your whale')} />

        <div className="modal-hero-box">
          <img
            src={getSpriteUrl('cheerful')}
            alt={t('鲸鱼娘', 'Whale character')}
            className="modal-hero-box__img"
          />
          <div style={{ fontSize: '0.82rem', lineHeight: 1.5, minWidth: 0 }}>
            <div style={{ fontWeight: 700, marginBottom: '0.25rem' }}>
              「你好呀人类！以后你就是我的长期饲养员啦～」
            </div>
            <div style={{ color: 'var(--nb-color-text-muted)', fontSize: '0.78rem' }}>
              {t(
                '💡 喜好：热腾腾的白米饭、Token 口粮、趴着摸鱼（随时可在左上角状态卡片修改名字哦～）',
                'She likes hot rice, token treats and lounging. You can rename her from the status card.',
              )}
            </div>
          </div>
        </div>

        <div style={{ display: 'flex', flexDirection: 'column', gap: '0.45rem' }}>
          <label
            htmlFor="whale-init-name"
            style={{ fontSize: '0.82rem', fontWeight: 700, color: 'var(--nb-color-text)' }}
          >
            {t(
              '她的专属昵称（最多 10 个字，点右侧骰子可随机可爱名字）：',
              'Nickname (up to 10 characters)',
            )}
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
              title={t('随机摇一个软萌可爱的名字', 'Choose a random name')}
            >
              <Dices
                size={16}
                style={{
                  transition: 'transform 0.28s cubic-bezier(0.34, 1.56, 0.64, 1)',
                  transform: `rotate(${diceSpin * 180}deg)`,
                }}
              />
              {t('随机取名', 'Random name')}
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
          {t('开放小筑，允许互相串门', 'Open my haven for mutual visits')}
        </label>
        <small>
          {t(
            '开放后可以互访。家里断粮时，饿了的小鲸可能自己去邻居家蹭饭。可随时在「串门」关门。',
            'Open havens can visit each other. If the bowl runs empty, your hungry whale may swim to a neighbor for a meal. You can close your door under Visits.',
          )}
        </small>
        <div className="notice-actions">
          <button disabled={busy} type="submit" className="nb-btn nb-btn--primary">
            <Sparkles size={15} />{' '}
            {t(
              `确认叫「${name.trim() || DEFAULT_WHALE_NAME}」，领养回家`,
              `Adopt ${name.trim() || DEFAULT_WHALE_NAME}`,
            )}
          </button>
        </div>
      </form>
    </div>
  );
}
