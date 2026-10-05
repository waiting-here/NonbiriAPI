import { queryPath } from '@shared/operations/api';
import type { Dataset, Selection } from './history';
import type { GameID } from './copy';

export function historyDownloadURL(
  game: GameID | 'blackjack',
  dataset: Dataset,
  selection: Selection = {},
) {
  return queryPath('/admin/api/games/' + game + '/history/download', { dataset, ...selection });
}
