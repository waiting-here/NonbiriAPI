import { memo, useCallback, useMemo, useRef, useState, type KeyboardEvent } from 'react';
import { useGameCopy } from '../copy';
import { TileGlyph } from './TileGlyph';
import { MatchEffect, type MatchAnimation } from './MatchEffect';
import type { LinkLinkCoordinate, LinkLinkHint, LinkLinkState } from './types';

const positionKey = (point: LinkLinkCoordinate) => `${point.row}:${point.col}`;

const LinkTile = memo(function LinkTile({
  row,
  col,
  glyph,
  removed,
  selected,
  pending,
  hinted,
  vanishing,
  tabIndex,
  hintLabel,
  onSelect,
  onFocus,
  onKeyDown,
  register,
}: {
  row: number;
  col: number;
  glyph: string;
  removed: boolean;
  selected: boolean;
  pending: boolean;
  hinted: boolean;
  vanishing: boolean;
  tabIndex: number;
  hintLabel: string;
  onSelect: (coordinate: LinkLinkCoordinate) => void;
  onFocus: (key: string) => void;
  onKeyDown: (event: KeyboardEvent<HTMLButtonElement>, row: number, col: number) => void;
  register: (key: string, node: HTMLButtonElement | null) => void;
}) {
  const key = `${row}:${col}`;
  const ref = useCallback((node: HTMLButtonElement | null) => register(key, node), [key, register]);
  return (
    <button
      type="button"
      role="gridcell"
      aria-rowindex={row + 1}
      aria-colindex={col + 1}
      aria-selected={selected || pending}
      aria-busy={pending || undefined}
      className={`linklink-tile${selected ? ' is-selected' : ''}${pending ? ' is-pending' : ''}${removed ? ' is-removed' : ''}${vanishing ? ' is-vanishing' : ''}${hinted ? ' is-hinted' : ''}`}
      style={{ gridRow: row + 1, gridColumn: col + 1 }}
      disabled={removed}
      tabIndex={tabIndex}
      ref={ref}
      onFocus={() => onFocus(key)}
      onClick={() => onSelect({ row, col })}
      onKeyDown={(event) => onKeyDown(event, row, col)}
    >
      {hinted ? <span className="linklink-hint-tag">{hintLabel}</span> : null}
      {removed && !vanishing ? <span aria-hidden="true" /> : <TileGlyph tileKey={glyph} />}
    </button>
  );
});

export const LinkBoard = memo(function LinkBoard({
  state,
  selected,
  busy,
  pending,
  animation,
  hint,
  onSelect,
}: {
  state: LinkLinkState;
  selected: LinkLinkCoordinate | null;
  busy: boolean;
  pending: readonly LinkLinkCoordinate[] | null;
  animation: MatchAnimation | null;
  hint: LinkLinkHint | null;
  onSelect: (coordinate: LinkLinkCoordinate) => void;
}) {
  const { text } = useGameCopy();
  const active = useMemo(
    () => state.board.tiles.filter((tile) => !tile.removed),
    [state.board.tiles],
  );
  const [focus, setFocus] = useState(() => (active[0] ? positionKey(active[0]) : ''));
  const refs = useRef(new Map<string, HTMLButtonElement>());
  const register = useCallback((key: string, node: HTMLButtonElement | null) => {
    if (node) refs.current.set(key, node);
    else refs.current.delete(key);
  }, []);
  const effectiveFocus = active.some((tile) => positionKey(tile) === focus)
    ? focus
    : active[0]
      ? positionKey(active[0])
      : '';
  const onKeyDown = useCallback(
    (event: KeyboardEvent<HTMLButtonElement>, row: number, col: number) => {
      const delta = { ArrowLeft: [0, -1], ArrowRight: [0, 1], ArrowUp: [-1, 0], ArrowDown: [1, 0] }[
        event.key
      ];
      if (!delta) return;
      event.preventDefault();
      const target = active.reduce<(typeof active)[number] | undefined>((best, tile) => {
        const distance = Math.abs(tile.row - row - delta[0]) + Math.abs(tile.col - col - delta[1]);
        return !best ||
          distance < Math.abs(best.row - row - delta[0]) + Math.abs(best.col - col - delta[1])
          ? tile
          : best;
      }, undefined);
      if (target) refs.current.get(positionKey(target))?.focus();
    },
    [active],
  );
  const before = useMemo(
    () => new Map(animation?.before.board.tiles.map((tile) => [positionKey(tile), tile.tileKey])),
    [animation],
  );
  const hintAnimation = useMemo(
    () =>
      hint
        ? {
            key: state.revision,
            before: state,
            pair: [hint.first, hint.second] as const,
            path: hint.path,
          }
        : null,
    [hint, state],
  );
  return (
    <div className={`linklink-board-scroll linklink-board-scroll--${state.spec}`}>
      <div
        className="linklink-board"
        style={{ '--linklink-cols': state.board.cols } as React.CSSProperties}
        role="grid"
        aria-rowcount={state.board.rows}
        aria-colcount={state.board.cols}
        aria-label={text('linklink.active')}
        aria-busy={busy}
      >
        {state.board.tiles.map((tile) => {
          const key = positionKey(tile);
          const vanishing = !!animation?.pair.some((point) => positionKey(point) === key);
          return (
            <LinkTile
              key={key}
              row={tile.row}
              col={tile.col}
              glyph={vanishing ? (before.get(key) ?? tile.tileKey) : tile.tileKey}
              removed={tile.removed}
              selected={!!selected && positionKey(selected) === key}
              pending={!!pending?.some((point) => positionKey(point) === key)}
              hinted={
                !!hint && [hint.first, hint.second].some((point) => positionKey(point) === key)
              }
              vanishing={vanishing}
              tabIndex={!tile.removed && key === effectiveFocus ? 0 : -1}
              hintLabel={text('linklink.hintTile')}
              onSelect={onSelect}
              onFocus={setFocus}
              onKeyDown={onKeyDown}
              register={register}
            />
          );
        })}
        {animation ? <MatchEffect key={animation.key} animation={animation} /> : null}
        {hintAnimation ? <MatchEffect hint animation={hintAnimation} /> : null}
      </div>
    </div>
  );
});
