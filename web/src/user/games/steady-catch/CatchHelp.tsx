import { useState } from 'react';
import { CatchDialog } from './CatchDialogs';
import { useCatchText } from './copy';

export function CatchHelp({ open, onClose }: { open: boolean; onClose: () => void }) {
  const t = useCatchText();
  const [page, setPage] = useState(0);
  const titles = [
    t('怎么玩', 'How to play'),
    t('卡片和道具', 'Cards and power-ups'),
    t('门票和奖励', 'Entry and rewards'),
  ];
  const pages = [
    [
      t('移动接物垫，在虚线处接住卡片。', 'Move the pad to catch cards at the dotted line.'),
      t(
        '电脑：鼠标、← → 或 A D 移动；空格护场。',
        'Desktop: mouse, arrows or A/D to move; Space for shield.',
      ),
      t(
        '手机：按住场内滑动，或长按方向按钮。',
        'Mobile: drag on the field or hold a direction button.',
      ),
      t('P 或 Esc 暂停；倒计时中按下可取消开局。', 'P or Esc pauses, or cancels the countdown.'),
      t(
        '坚持 90 秒，保有耐心并拿到 600 分即可通关。',
        'Survive 90 seconds with health remaining and 600 points.',
      ),
      t('卡片内容是中文网络梗。', 'Phrases are Chinese internet memes and are shown in Chinese.'),
    ],
    [
      t(
        '白卡 10 分，金卡 20 分；每连击 5 句提升倍率，最高 ×3。',
        'White: 10 points; gold: 20. Every five catches increase the multiplier, up to ×3.',
      ),
      t(
        '躲开红卡！受伤或漏接中断连击，漏接不扣耐心。',
        'Avoid red cards! Damage and misses break combos. Misses cost no health.',
      ),
      t('上下文护盾挡住错误卡，持续 7 秒。', 'Context shield blocks hazards for seven seconds.'),
      t(
        '低温采样降速、注意力磁铁吸卡，均持续 7 秒。',
        'Low temperature slows cards; the magnet attracts phrases. Both last seven seconds.',
      ),
      t(
        'Token 翻倍加分 7 秒；重新生成回 1 点耐心，上限 5 点。',
        'Double tokens doubles scores for seven seconds; Regenerate restores one health, up to five.',
      ),
      t(
        '每接 10 句充满护场，按按钮或空格释放 4 秒护盾。',
        'Ten catches charge a four-second shield. Save it until needed, then tap or press Space.',
      ),
    ],
    [
      t(
        '门票先扣游戏积分，不足时补通用积分。',
        'Entry uses game credits first, then general credits.',
      ),
      t(
        '只有首次通关发奖励；局内分数不等于钱包积分。',
        'Only the first clear gives a reward. Game score is separate from wallet credit.',
      ),
      t(
        '正常结束或放弃不退票，系统中止原路退票。',
        'Completion and abandonment do not refund entry. System cancellation does.',
      ),
      t(
        '切换窗口自动暂停，暂停或离线不推进游戏。',
        'Switching windows pauses play. Pausing or going offline stops the game clock.',
      ),
      t('本局从创建起保留 30 分钟。', 'A session lasts 30 minutes from creation.'),
    ],
  ];
  return (
    <CatchDialog
      open={open}
      onClose={onClose}
      title={t('怎么稳稳接住', 'How to catch steadily')}
      className="help-dialog"
    >
      <section className="help-page" aria-label={titles[page]}>
        <h3>{titles[page]}</h3>
        <ul>
          {pages[page].map((line) => (
            <li key={line}>{line}</li>
          ))}
        </ul>
      </section>
      <nav className="help-pages" aria-label={t('说明分页', 'Help pages')}>
        {titles.map((title, index) => (
          <button
            key={title}
            className="icon-button"
            aria-label={title}
            aria-current={page === index ? 'page' : undefined}
            onClick={() => setPage(index)}
          >
            <span aria-hidden="true" />
          </button>
        ))}
      </nav>
    </CatchDialog>
  );
}
