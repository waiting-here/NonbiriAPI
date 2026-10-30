import { useEffect, useState } from 'react';
import { Field, Fields, Toggle } from '@shared/components/ui';
import { polygonError } from './polygonEdit';
import type { Level, Point, Shape, Switch, Gate, Direction } from '@shared/fatfish/engine/types';
import { toolNames } from '@shared/fatfish/art';
import { toolCopy, useFatFishText } from './copy';
import { NumberField } from './NumberField';
import { HeadingInput } from './HeadingInput';
import { describeIssue, objectLabels } from './editorState';
import { cloneLevel, localValidation, px, rect, type Selection, unit } from './draft';

type Editable = Shape & {
  resource_key?: string;
  placed?: boolean;
  x?: number;
  y?: number;
  required?: number;
  capacity?: number;
  mode?: Switch['mode'] | Gate['mode'] | Direction['mode'];
  initially_open?: boolean;
  switch_ids?: number[];
  heading?: number;
};
function itemFor(level: Level, selected: Selection): Editable | null {
  if (selected.kind === 'fish') return null;
  return (
    (level[selected.kind].find((item) => item.id === selected.id) as Editable | undefined) ?? null
  );
}
function PointFields({
  label,
  value,
  change,
}: {
  label: string;
  value: Point;
  change(value: Point): void;
}) {
  return (
    <Fields legend={label}>
      <NumberField
        step={1 / 64}
        label="X"
        value={px(value.x)}
        onChange={(x) => change({ ...value, x: unit(x) })}
      />
      <NumberField
        step={1 / 64}
        label="Y"
        value={px(value.y)}
        onChange={(y) => change({ ...value, y: unit(y) })}
      />
    </Fields>
  );
}

export function ShapeInspector({
  level,
  selected,
  commit,
  onDelete,
  onPendingChange,
}: {
  level: Level;
  selected: Selection | null;
  commit(next: Level): void;
  onDelete(): void;
  onPendingChange?(pending: boolean): void;
}) {
  const text = useFatFishText();
  const [pending, setPending] = useState<{
    selection: Selection;
    level: Level;
    error: string;
  } | null>(null);
  const temporary =
    pending?.selection.kind === selected?.kind && pending?.selection.id === selected?.id
      ? pending
      : null;
  const shown = temporary?.level ?? level;
  useEffect(() => {
    onPendingChange?.(!!temporary);
  }, [temporary, onPendingChange]);
  const apply = (next: Level) => {
    if (!selected) return;
    const error =
      selected.kind !== 'fish'
        ? (polygonError(next, selected) ?? localValidation(next))
        : localValidation(next);
    if (error) setPending({ selection: selected, level: next, error });
    else {
      setPending(null);
      commit(next);
    }
  };
  if (!selected) return <p>{text('select_an_object_on_the_map_or_add_one_above')}</p>;
  const mutateFish = (change: (fish: Level['fish'][number]) => void) => {
    const next = cloneLevel(shown),
      fish = next.fish.find((item) => item.id === selected.id);
    if (fish) {
      change(fish);
      apply(next);
    }
  };
  const mutate = (change: (item: Editable) => void) => {
    if (selected.kind === 'fish') return;
    const next = cloneLevel(shown),
      item = itemFor(next, selected);
    if (item) {
      change(item);
      apply(next);
    }
  };
  const fish =
    selected.kind === 'fish' ? shown.fish.find((item) => item.id === selected.id) : undefined;
  const item = selected.kind === 'fish' ? null : itemFor(shown, selected);
  const editVertex = (hole: number | null, index: number, value: Point) =>
    mutate((shape) => {
      const ring = hole === null ? shape.polygon.outer : shape.polygon.holes[hole];
      ring[index] = value;
    });
  const addVertex = (hole: number | null) =>
    mutate((shape) => {
      const ring = hole === null ? shape.polygon.outer : shape.polygon.holes[hole];
      if (ring.length >= (hole === null ? 128 : 64)) return;
      const a = ring[ring.length - 1],
        b = ring[0];
      ring.push({ x: Math.round((a.x + b.x) / 2), y: Math.round((a.y + b.y) / 2) });
    });
  const removeVertex = (hole: number | null, index: number) =>
    mutate((shape) => {
      const ring = hole === null ? shape.polygon.outer : shape.polygon.holes[hole];
      if (ring.length > 3) ring.splice(index, 1);
    });
  const renderRing = (ring: Point[], hole: number | null) => (
    <fieldset key={hole ?? 'outer'}>
      <legend>{hole === null ? text('outer_contour') : `${text('hole')} ${hole + 1}`}</legend>
      {ring.map((vertex, index) => (
        <div className="fatfish-vertex" key={index}>
          <PointFields
            label={`${index + 1}`}
            value={vertex}
            change={(point) => editVertex(hole, index, point)}
          />
          <button
            type="button"
            disabled={ring.length <= 3}
            onClick={() => removeVertex(hole, index)}
            aria-label={`${text('remove_vertex')} ${index + 1}`}
          >
            ×
          </button>
        </div>
      ))}
      <button
        type="button"
        disabled={ring.length >= (hole === null ? 128 : 64)}
        onClick={() => addVertex(hole)}
      >
        {text('add_vertex')}
      </button>
      {hole !== null ? (
        <button
          type="button"
          onClick={() =>
            mutate((shape) => {
              shape.polygon.holes.splice(hole, 1);
            })
          }
        >
          {text('remove_hole')}
        </button>
      ) : null}
    </fieldset>
  );
  return (
    <section
      onKeyDown={(event) => {
        if (event.key === 'Escape') setPending(null);
      }}
      className="fatfish-inspector"
      aria-label={text('object_properties')}
    >
      <h3>
        {text(objectLabels[selected.kind])} #{selected.id}
      </h3>
      {temporary ? (
        <div role="alert" className="fatfish-error">
          <p>{describeIssue(temporary.error, text)}</p>
          <details>
            <summary>{text('technical_details')}</summary>
            {temporary.error}
          </details>
        </div>
      ) : null}
      <Fields>
        {fish ? (
          <>
            <PointFields
              label={text('starting_position')}
              value={fish}
              change={(point) =>
                mutateFish((entry) => {
                  entry.x = point.x;
                  entry.y = point.y;
                })
              }
            />
            <HeadingInput
              label={text('heading')}
              value={fish.heading}
              onChange={(value) =>
                mutateFish((entry) => {
                  entry.heading = value;
                })
              }
            />
          </>
        ) : null}
        {item && selected.kind === 'tools' ? (
          <>
            <Field label={text('tool_type')}>
              {(props) => (
                <select
                  {...props}
                  value={item.resource_key}
                  onChange={(event) =>
                    mutate((shape) => {
                      shape.resource_key = event.target.value;
                    })
                  }
                >
                  {Object.keys(toolNames).map((key) => (
                    <option key={key} value={key}>
                      {text(toolCopy[key])}
                    </option>
                  ))}
                </select>
              )}
            </Field>
            <Toggle
              label={text('active_at_start_field_or_workbench')}
              checked={!!item.placed}
              onChange={(placed) =>
                mutate((shape) => {
                  shape.placed = placed;
                })
              }
            />
            <PointFields
              label={text('placed_center')}
              value={{ x: item.x ?? 0, y: item.y ?? 0 }}
              change={(point) =>
                mutate((shape) => {
                  shape.x = point.x;
                  shape.y = point.y;
                })
              }
            />
          </>
        ) : null}
        {item && selected.kind === 'bowls' ? (
          <>
            <NumberField
              label={text('required_fish')}
              value={item.required ?? 0}
              min={0}
              max={40}
              onChange={(value) =>
                mutate((shape) => {
                  shape.required = value;
                })
              }
            />
            <NumberField
              label={text('capacity')}
              value={item.capacity ?? 1}
              min={1}
              max={40}
              onChange={(value) =>
                mutate((shape) => {
                  shape.capacity = value;
                })
              }
            />
          </>
        ) : null}
        {item && selected.kind === 'switches' ? (
          <Field size="short" label={text('switch_mode')}>
            {(props) => (
              <select
                {...props}
                value={item.mode}
                onChange={(event) =>
                  mutate((shape) => {
                    shape.mode = event.target.value as Switch['mode'];
                  })
                }
              >
                <option value="latch">{text('latch')}</option>
                <option value="hold">{text('hold')}</option>
              </select>
            )}
          </Field>
        ) : null}
        {item && selected.kind === 'gates' ? (
          <>
            <Toggle
              label={text('initially_open')}
              checked={!!item.initially_open}
              onChange={(open) =>
                mutate((shape) => {
                  shape.initially_open = open;
                })
              }
            />
            <Field size="short" label={text('control_rule')}>
              {(props) => (
                <select
                  {...props}
                  value={item.mode}
                  onChange={(event) =>
                    mutate((shape) => {
                      shape.mode = event.target.value as Gate['mode'];
                    })
                  }
                >
                  <option value="any">{text('any_switch')}</option>
                  <option value="all">{text('all_switches')}</option>
                </select>
              )}
            </Field>
            <fieldset>
              <legend>{text('linked_switches')}</legend>
              {level.switches.map((entry) => (
                <Field size="short" key={entry.id} label={`#${entry.id}`}>
                  {(props) => (
                    <input
                      {...props}
                      type="checkbox"
                      checked={item.switch_ids?.includes(entry.id) ?? false}
                      onChange={(event) =>
                        mutate((shape) => {
                          const ids = shape.switch_ids ?? [];
                          shape.switch_ids = event.target.checked
                            ? [...ids, entry.id]
                            : ids.filter((id) => id !== entry.id);
                        })
                      }
                    />
                  )}
                </Field>
              ))}
            </fieldset>
          </>
        ) : null}
        {item && selected.kind === 'directions' ? (
          <>
            <Field size="short" label={text('direction_mode')}>
              {(props) => (
                <select
                  {...props}
                  value={item.mode}
                  onChange={(event) =>
                    mutate((shape) => {
                      shape.mode = event.target.value as Direction['mode'];
                    })
                  }
                >
                  <option value="entry">{text('redirect_on_entry')}</option>
                  <option value="oneway">{text('one_way')}</option>
                </select>
              )}
            </Field>
            <HeadingInput
              label={text('heading')}
              value={item.heading ?? 0}
              onChange={(value) =>
                mutate((shape) => {
                  shape.heading = value;
                })
              }
            />
          </>
        ) : null}
      </Fields>
      {item ? (
        <details>
          <summary>{text('advanced_contours_and_holes')}</summary>
          <p>
            {selected.kind === 'tools'
              ? text('contour_coordinates_are_relative_to_the_tool_center')
              : text('contour_coordinates_use_field_coordinates')}
          </p>
          {renderRing(item.polygon.outer, null)}
          {item.polygon.holes.map((hole, index) => renderRing(hole, index))}
          <button
            type="button"
            disabled={item.polygon.holes.length >= 8}
            onClick={() =>
              mutate((shape) => {
                const xs = shape.polygon.outer.map((point) => point.x),
                  ys = shape.polygon.outer.map((point) => point.y);
                const x = (Math.min(...xs) + Math.max(...xs)) / 2,
                  y = (Math.min(...ys) + Math.max(...ys)) / 2;
                shape.polygon.holes.push(
                  rect(Math.round(x), Math.round(y), unit(12), unit(12)).outer,
                );
              })
            }
          >
            {text('add_hole')}
          </button>
        </details>
      ) : null}
      <button type="button" className="fatfish-danger" onClick={onDelete}>
        {text('delete_object')}
      </button>
    </section>
  );
}
