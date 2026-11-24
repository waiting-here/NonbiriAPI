import { skipToken, useQuery, useQueryClient } from '@tanstack/react-query';
import { useState } from 'react';
import { useLocation } from 'react-router';
import type { NativeService } from '@shared/nativeServices';
import { listReturnPath } from '@shared/operations/listReturn';
import { resourceNavigation } from '@shared/operations/resourceNavigation';
import type { Connection } from '../connections/types';
import { coreKeys } from '../queries';
import type {
  BindingCandidate,
  BindingSelection,
  CatalogSourceType,
  Endpoint,
  EndpointKey,
} from '../types';

export interface SourcePickerView {
  service: Connection | null;
  endpoint: Endpoint;
  entryKey: EndpointKey;
  serviceSearch: string;
  endpointSearch: string;
  keySearch: string;
  modelSearch: string;
  source: CatalogSourceType | '';
  step: 'endpoints' | 'keys' | 'models';
  pages: { service: string; endpoint: string; key: string; model: string };
}

interface SourceSelection {
  selections: BindingSelection[];
  details: Record<string, BindingCandidate>;
  nativeServices: Record<string, NativeService[]>;
}

interface SourceVisit extends SourceSelection {
  entry: string;
  path: string;
  modelId: string;
  view: SourcePickerView;
}

export function useModelSourceVisit(accountId: string, modelId: string) {
  const location = useLocation();
  const client = useQueryClient();
  const key = [
    ...coreKeys.account(accountId),
    'source-picker-visits',
    resourceNavigation(client).epoch,
  ];
  const { data: visits } = useQuery<SourceVisit[]>({
    queryKey: key,
    queryFn: skipToken,
    gcTime: Infinity,
  });
  const path = location.pathname + location.search;
  const [restored] = useState(() => {
    const matching = visits?.filter((visit) => visit.path === path && visit.modelId === modelId);
    return (
      matching?.find((visit) => visit.entry === location.key) ??
      matching?.find((visit) => visit.entry === location.state?.modelSourceEntry)
    );
  });
  const remember = (view: SourcePickerView, selection: SourceSelection) => {
    const visit = { ...selection, view, modelId, path, entry: location.key };
    client.setQueryData<SourceVisit[]>(key, (previous = []) =>
      [visit, ...previous.filter((row) => row.entry !== visit.entry)].slice(0, 32),
    );
  };
  const endpointState = {
    returnTo: path,
    modelSourceEntry: location.key,
    modelSourceParent: listReturnPath(location.state, '/models', '/issues'),
  };
  return { restored, remember, endpointState };
}
