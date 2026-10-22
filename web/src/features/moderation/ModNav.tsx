// The moderator pages as tabs (queue, log, account recovery, and for admins
// the roles page), shown on each of them to moderators only.
import { useTranslation } from 'react-i18next';
import { useMe } from '../../app/session';
import { SubNav, type SubNavItem } from '../../app/SubNav';

export function ModNav() {
  const { t } = useTranslation('moderation');
  const role = useMe()?.role;
  if (role !== 'moderator' && role !== 'admin') return null;
  const items: SubNavItem[] = [
    { to: '/mod', label: t('nav.queue') },
    { to: '/mod/log', label: t('nav.log') },
    { to: '/mod/recovery', label: t('nav.recovery') },
  ];
  if (role === 'admin') items.push({ to: '/mod/roles', label: t('nav.roles'), testId: 'queue-roles-link' });
  return <SubNav label={t('nav.label')} items={items} />;
}
