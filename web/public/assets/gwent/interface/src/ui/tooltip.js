/* eslint-disable */
// Shared themed hints, including cards inside modal dialogs.
let layer, active, timer, pointerTarget, focusTarget;
function hide() {
  clearTimeout(timer);
  if (!layer) return;
  if (layer.hidePopover && layer.matches(':popover-open')) layer.hidePopover();
  layer.hidden = true;
  if (active) {
    const ids = (active.getAttribute('aria-describedby') || '')
      .split(/\s+/)
      .filter((id) => id && id !== layer.id);
    if (ids.length) active.setAttribute('aria-describedby', ids.join(' '));
    else active.removeAttribute('aria-describedby');
  }
  active = null;
}
function show(target) {
  hide();
  if (!target?.isConnected || !target.dataset.tooltip) return;
  active = target;
  layer.textContent = target.dataset.tooltip;
  layer.style.setProperty(
    '--hint-color',
    target.style.getPropertyValue('--card-color') || 'var(--selected-color, #77e4c8)',
  );
  layer.hidden = false;
  if (layer.showPopover) layer.showPopover();
  else (target.closest('dialog[open]') || document.body).append(layer);
  const rect = target.getBoundingClientRect();
  const width = layer.offsetWidth,
    height = layer.offsetHeight;
  let left = rect.right + 12;
  if (left + width > innerWidth - 12) left = rect.left - width - 12;
  left = Math.max(12, Math.min(left, innerWidth - width - 12));
  let top = rect.width > width * 1.5 ? rect.bottom + 10 : rect.top;
  top = Math.max(12, Math.min(top, innerHeight - height - 12));
  layer.style.left = `${left}px`;
  layer.style.top = `${top}px`;
  const ids = (target.getAttribute('aria-describedby') || '').split(/\s+/).filter(Boolean);
  target.setAttribute('aria-describedby', [...new Set([...ids, layer.id])].join(' '));
}
function queue(target) {
  hide();
  if (target) timer = setTimeout(() => show(target), 220);
}
function initialize() {
  if (layer) return;
  layer = document.createElement('div');
  layer.id = 'arena-tooltip';
  layer.className = 'arena-tooltip';
  layer.setAttribute('role', 'tooltip');
  layer.hidden = true;
  if (layer.showPopover) layer.setAttribute('popover', 'manual');
  document.body.append(layer);
  const trackPointer = (event) => {
    if (event.pointerType === 'touch' || matchMedia('(hover: none)').matches) {
      pointerTarget = null;
      hide();
      return;
    }
    const target = event.target.closest?.('[data-tooltip]');
    if (target === pointerTarget) return;
    pointerTarget = target;
    queue(target || focusTarget);
  };
  document.addEventListener('pointerover', trackPointer);
  document.addEventListener('pointermove', trackPointer);
  document.addEventListener('pointerout', (event) => {
    if (!pointerTarget || pointerTarget.contains(event.relatedTarget)) return;
    pointerTarget = event.relatedTarget?.closest?.('[data-tooltip]');
    queue(pointerTarget || focusTarget);
  });
  document.addEventListener('focusin', (event) => {
    focusTarget = event.target.matches(':focus-visible')
      ? event.target.closest?.('[data-tooltip]')
      : null;
    if (focusTarget) show(focusTarget);
  });
  document.addEventListener('focusout', () => {
    focusTarget = null;
    queue(pointerTarget);
  });
  document.addEventListener('pointerdown', hide);
  document.addEventListener('keydown', (event) => {
    if (event.key === 'Escape') hide();
  });
  const hideAfterLayout = () => {
    // Auto-scrolling a hovered hand into view must not cancel its pending hint.
    pointerTarget = pointerTarget?.matches(':hover') ? pointerTarget : null;
    queue(pointerTarget);
  };
  document.addEventListener('scroll', hideAfterLayout, true);
  document.addEventListener('close', hide, true);
  window.addEventListener('resize', hideAfterLayout);
}
export function setTooltip(element, text) {
  initialize();
  element.removeAttribute('title');
  element.dataset.tooltip = text;
  if (active === element) layer.textContent = text;
}
export function showTooltip(element) {
  initialize();
  show(element);
}
