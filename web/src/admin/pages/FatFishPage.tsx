import { useSearchParams } from 'react-router';
import { Card, PageHeader } from '@shared/components/States';
import { useActivityText } from '@shared/limitedactivities/copy';
import { LevelManager } from '../features/fatfish/LevelEditor';
import { PeriodManager } from '../features/fatfish/PeriodEditor';
import '../features/fatfish/fatfish.css';

export function FatFishPage() {
  const t = useActivityText();
  const [params, setParams] = useSearchParams();
  const tab = params.get('tab') === 'periods' ? 'periods' : 'levels';
  return <div className="page fatfish-admin">
    <PageHeader title={t('饲养大肥鱼', 'Fat Fish')} icon="activities"
      description={t('编辑关卡并编排期次。正式期次由管理员明确发布。', 'Edit levels and arrange periods. An administrator explicitly publishes a formal period.')} />
    <Card>
      <nav className="fatfish-tabs" aria-label={t('大肥鱼管理', 'Fat Fish administration')}>
        <button type="button" aria-current={tab === 'levels' ? 'page' : undefined} onClick={() => setParams({ tab: 'levels' })}>{t('关卡与版本', 'Levels and versions')}</button>
        <button type="button" aria-current={tab === 'periods' ? 'page' : undefined} onClick={() => setParams({ tab: 'periods' })}>{t('期次与节点', 'Periods and nodes')}</button>
      </nav>
      {tab === 'levels' ? <LevelManager /> : <PeriodManager />}
    </Card>
  </div>;
}
