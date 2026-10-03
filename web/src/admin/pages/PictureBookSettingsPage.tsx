import { LimitedActivitiesPage } from './LimitedActivitiesPage';
import { PictureBookAdmin } from '../features/picturebook/PictureBookAdmin';

export function PictureBookSettingsPage() {
  return (
    <LimitedActivitiesPage>
      {(section) => <PictureBookAdmin section={section} />}
    </LimitedActivitiesPage>
  );
}
