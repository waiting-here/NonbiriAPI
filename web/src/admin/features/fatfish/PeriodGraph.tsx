import { useQuery, useQueryClient } from '@tanstack/react-query';
import { ErrorState, LoadingState } from '@shared/components/States';
import { useFatFishText } from './copy';
import { responseOutcomeUnknown } from '@shared/operations/api';
import { useRetainedOperation } from '@shared/operations/useRetainedOperation';
import { getGraphLayout, saveGraphLayout, type Condition, type GraphPosition, type PeriodRecord } from './api';
import { NodeMap } from './NodeMap';
import type { ConditionPath } from './conditionEdges';

export function PeriodGraph({ period, selectedID, selectedCondition, onSelect, onConnect, onRemove }: {
  period: PeriodRecord; selectedID: string | null; selectedCondition?: Condition;
  onSelect(id: string): boolean | Promise<boolean>; onConnect(source: string, target: string): void;
  onRemove(target: string, path: ConditionPath): void;
}) {
  const text = useFatFishText(), client = useQueryClient();
  const layout = useQuery({ queryKey: ['fatfish', 'layout', period.id], queryFn: () => getGraphLayout(period.id) });
  const save = useRetainedOperation(async (input: { expected_revision: string; nodes: GraphPosition[] }, key, context) => {
    const result = await saveGraphLayout(period.id, input, key);
    context.commit(() => client.setQueryData(['fatfish', 'layout', period.id], result));
    return result;
  }, (_input, error) => error ? client.invalidateQueries({ queryKey: ['fatfish', 'layout', period.id] }) : undefined, ['admin', 'fatfish']);
  const uncertain = save.isError && responseOutcomeUnknown(save.error);
  const positions = save.isPending || uncertain ? save.variables?.nodes : layout.data?.nodes;
  const nodes = (period.nodes ?? []).map((node) => {
    const position = positions?.find((entry) => entry.node_id === node.id);
    return position ? { ...node, map_x: position.map_x, map_y: position.map_y } : node;
  });
  if (layout.isPending) return <LoadingState />;
  if (layout.error) return <ErrorState error={layout.error} onRetry={() => void layout.refetch()} />;
  return <section>
    <div inert={save.isPending || uncertain}>
      <NodeMap nodes={nodes} selectedID={selectedID} selectedCondition={selectedCondition}
        onSelect={onSelect} onConnect={onConnect} onRemove={onRemove} onMove={(id, x, y) => {
          const input = { expected_revision: layout.data!.revision, nodes: nodes.map((node) => ({
            node_id: node.id, map_x: node.id === id ? x : node.map_x, map_y: node.id === id ? y : node.map_y,
          })) };
          void save.mutateAsync(input).catch(() => undefined);
        }} />
    </div>
    {save.error ? <ErrorState error={save.error} /> : null}
    {uncertain ? <button type="button" onClick={() => { if (save.variables) void save.mutateAsync(save.variables).catch(() => undefined); }}>{text('retry_the_same_layout_save')}</button> : null}
  </section>;
}
