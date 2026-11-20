export interface RouteObservation {
  reason: string;
  binding_id?: string;
  native_service?: string;
  match?: 'native' | 'conversion';
  stage?: 'claim' | 'dispatch' | 'queue' | 'accept';
  field?: string;
}

export interface RouteDecision {
  observations: RouteObservation[];
  truncated: boolean;
}

// Routing observations are supplementary. Older or partially readable records
// still have independently useful request and accounting data.
export function normalizeRouteDecision(value: unknown): RouteDecision | undefined {
  if (!value || typeof value !== 'object' || !('observations' in value)) return;
  if (!Array.isArray(value.observations)) return;
  const observations: RouteObservation[] = [];
  let truncated = 'truncated' in value && value.truncated === true;
  for (const item of value.observations) {
    if (!item || typeof item !== 'object' || typeof item.reason !== 'string' || !item.reason) {
      truncated = true;
      continue;
    }
    observations.push({
      reason: item.reason,
      binding_id: typeof item.binding_id === 'string' ? item.binding_id : undefined,
      native_service: typeof item.native_service === 'string' ? item.native_service : undefined,
      match: item.match === 'native' || item.match === 'conversion' ? item.match : undefined,
      stage:
        item.stage === 'claim' ||
        item.stage === 'dispatch' ||
        item.stage === 'queue' ||
        item.stage === 'accept'
          ? item.stage
          : undefined,
      field: typeof item.field === 'string' ? item.field : undefined,
    });
  }
  return { observations, truncated };
}
