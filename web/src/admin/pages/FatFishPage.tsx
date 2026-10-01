import { useSearchParams } from 'react-router';
import { Card, PageHeader } from '@shared/components/States';
import { useActivityText } from '@shared/limitedactivities/copy';
import { LevelManager } from '../features/fatfish/LevelEditor';
import { PeriodManager } from '../features/fatfish/PeriodEditor';
import '../features/fatfish/fatfish.css';
export function FatFishPage() {
  const text = useActivityText();
  const [params, setParams] = useSearchParams();
  const tab = params.get('tab') === 'periods' ? 'periods' : 'levels';
  return (
    <div className="page fatfish-admin">
      <PageHeader
        title={text('common.fatFish')}
        icon="activities"
        description={text('common.editLevelsAndArrangePeriodsAnAdministrator')}
      />
      <Card>
        <nav className="fatfish-tabs" aria-label={text('common.fatFishAdministration')}>
          <button
            type="button"
            aria-current={tab === 'levels' ? 'page' : undefined}
            onClick={() => setParams({ tab: 'levels' })}
          >
            {text('common.levelsAndVersions')}
          </button>
          <button
            type="button"
            aria-current={tab === 'periods' ? 'page' : undefined}
            onClick={() => setParams({ tab: 'periods' })}
          >
            {text('common.periodsAndNodes')}
          </button>
        </nav>
        {tab === 'levels' ? <LevelManager /> : <PeriodManager />}
      </Card>
    </div>
  );
}
