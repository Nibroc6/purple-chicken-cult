// The user's own post lists (Saved, Seen) as tabs (SCAFFOLD); each list page
// renders it under its title. A new list page adds its entry here.
import { useTranslation } from 'react-i18next';
import { SubNav } from './SubNav';

export function PostListsNav() {
  const { t } = useTranslation('common');
  return (
    <SubNav
      label={t('postLists.label')}
      items={[
        { to: '/saves', label: t('nav.saves') },
        { to: '/seen', label: t('nav.seen') },
      ]}
    />
  );
}
