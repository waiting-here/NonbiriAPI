import { useState } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { Card, ErrorState, LoadingState } from '@shared/components/States';
import { getDetail, getWallet } from '@shared/limitedactivities/api';
import { usePictureBookText } from '@shared/picturebook/copy';
import { useUserSession } from '../../data';
import { economySessionRequest } from '../economy/queries';
import { limitedActivityKeys } from '../limitedactivities/queries';
import { ImagePolicyNotice } from './ImagePolicyNotice';
import { ModelForm } from './ModelForm';
import { TaskQueue } from './TaskQueue';
import { TaskDetail, TaskHistory } from './TaskHistory';
import { pictureBookKeys, useImageModels } from './queries';
import '@shared/picturebook/picturebook.css';

function Experience({ account }: { readonly account: string }) {
  const t = usePictureBookText(),
    client = useQueryClient(),
    models = useImageModels(account);
  const [selected, setSelected] = useState('');
  const detail = useQuery({
    queryKey: limitedActivityKeys.detail(account),
    queryFn: () => economySessionRequest(client, getDetail, account),
  });
  const wallet = useQuery({
    queryKey: limitedActivityKeys.wallet(account),
    queryFn: () => economySessionRequest(client, getWallet, account),
  });
  const catalog = models.data?.pages.flatMap((page) => page.data) ?? [];
  return (
    <div className="picturebook-stack">
      <ImagePolicyNotice />
      {models.isPending ? (
        <LoadingState />
      ) : models.error ? (
        <ErrorState error={models.error} onRetry={() => void models.refetch()} />
      ) : null}
      {detail.error ? (
        <ErrorState error={detail.error} onRetry={() => void detail.refetch()} />
      ) : null}
      {wallet.error ? (
        <ErrorState error={wallet.error} onRetry={() => void wallet.refetch()} />
      ) : null}
      {models.data ? (
        <ModelForm
          account={account}
          models={catalog}
          available={detail.data?.status === 'open' && !detail.error && !models.error}
          wallet={wallet.error ? undefined : wallet.data}
          onAccepted={(task) => {
            client.setQueryData(pictureBookKeys.task(account, task.id), task);
            setSelected(task.id);
          }}
        />
      ) : null}
      {models.hasNextPage ? (
        <button
          className="btn btn-secondary"
          disabled={models.isFetchingNextPage}
          onClick={() => void models.fetchNextPage()}
        >
          {t('载入更多模型', 'Load more models')}
        </button>
      ) : null}
      {models.data && catalog.length === 0 ? (
        <Card>
          <p>{t('暂无已开放的图像模型。', 'No image models have been made available.')}</p>
        </Card>
      ) : null}
      <TaskQueue account={account} onSelect={setSelected} />
      {selected ? (
        <TaskDetail key={account + ':' + selected} account={account} id={selected} />
      ) : null}
      <TaskHistory account={account} onSelect={setSelected} />
    </div>
  );
}
/** Compose inside PictureBookPage so the common activity and exchange controls remain shared. */
export function PictureBookExperience() {
  const session = useUserSession(),
    account = session.data?.user.id;
  return account && !session.error ? <Experience key={account} account={account} /> : null;
}
