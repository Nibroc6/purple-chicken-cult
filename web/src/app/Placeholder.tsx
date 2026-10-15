// Placeholder body for pages whose work package has not landed yet.
import { useTranslation } from 'react-i18next';

export function Placeholder({ title }: { title: string }) {
  const { t } = useTranslation('common');
  return (
    <section className="page">
      <h1>{title}</h1>
      <p className="muted">{t('placeholder.body')}</p>
    </section>
  );
}
