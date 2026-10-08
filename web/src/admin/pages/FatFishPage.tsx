import { Tabs } from '@shared/components/ui/Tabs';
import { Link, useSearchParams } from 'react-router';
import { usePictureBookText } from '@shared/picturebook/copy';
import { Card, PageHeader } from '@shared/components/States';
import { useActivityText } from '@shared/limitedactivities/copy';
import { LevelManager } from '../features/fatfish/LevelEditor';
import { PeriodManager } from '../features/fatfish/PeriodEditor';
import '../features/fatfish/fatfish.css';
export function FatFishPage() {
  const text = useActivityText();
  const copy = usePictureBookText();
  const [params, setParams] = useSearchParams();
  const tab = params.get('tab') === 'periods' ? 'periods' : 'levels';
  return (
    <div className="page fatfish-admin">
      <PageHeader
        title={text('common.fatFish')}
        icon="activities"
        description={text('common.editLevelsAndArrangePeriodsAnAdministrator')}
        back={
          <Link to="/limited-activities">{copy('返回限时活动', 'Back to limited activities')}</Link>
        }
      />
      <Card>
        <Tabs
          label={text('common.fatFishAdministration')}
          value={tab}
          onChange={(tab) => setParams({ tab })}
          tabs={[
            { value: 'levels', label: text('common.levelsAndVersions') },
            { value: 'periods', label: text('common.periodsAndNodes') },
          ]}
        />
        {tab === 'levels' ? <LevelManager /> : <PeriodManager />}
      </Card>
    </div>
  );
}
