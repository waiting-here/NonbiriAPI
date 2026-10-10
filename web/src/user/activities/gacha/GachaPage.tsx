import { useMemo } from 'react';
import { useQueryClient } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';
import { Link } from 'react-router';
import { UserPageGate } from '../../components/UserPageGate';
import { useUserSession } from '../../data';
import { GachaApp } from './GachaApp';
import { createGachaPort } from './api';

function AccountActivity({ account }: { account: string }) {
  const client = useQueryClient();
  const { i18n } = useTranslation();
  const english = i18n.language.startsWith('en');
  const port = useMemo(() => createGachaPort(client, account), [client, account]);
  return (
    <GachaApp
      port={port}
      english={english}
      back={
        <Link className="nb-btn nb-btn--ghost" to="/activities">
          {english ? 'Back to activities' : '返回活动'}
        </Link>
      }
    />
  );
}
export function GachaPage() {
  const session = useUserSession();
  return (
    <UserPageGate>
      {session.data?.user && (
        <AccountActivity key={session.data.user.id} account={session.data.user.id} />
      )}
    </UserPageGate>
  );
}
