// Privacy toggles (§4.5 chains, §6 suggestions, §5 likes, §10.1.4 slippery).
// Owner: P0-W11 → P1-W10.
import { useId, useState, type SyntheticEvent } from 'react';
import { useTranslation } from 'react-i18next';
import type { MeView } from '../../app/session';
import { SaveBar, SettingsShell, useSaveSettings } from './shared';

type Toggle = 'name_in_chains' | 'suggestible' | 'named_in_likes' | 'slippery_default';

const toggles = [
  { key: 'name_in_chains', label: 'privacy.nameInChains.label', help: 'privacy.nameInChains.help' },
  { key: 'suggestible', label: 'privacy.suggestible.label', help: 'privacy.suggestible.help' },
  { key: 'named_in_likes', label: 'privacy.namedInLikes.label', help: 'privacy.namedInLikes.help' },
  { key: 'slippery_default', label: 'privacy.slipperyDefault.label', help: 'privacy.slipperyDefault.help' },
] as const;

function PrivacyForm({ me }: { me: MeView }) {
  const { t } = useTranslation('settings');
  const id = useId();
  const [values, setValues] = useState<Record<Toggle, boolean>>({
    name_in_chains: me.settings.name_in_chains,
    suggestible: me.settings.suggestible,
    named_in_likes: me.settings.named_in_likes,
    slippery_default: me.settings.slippery_default,
  });
  const save = useSaveSettings();

  const onSubmit = (e: SyntheticEvent) => {
    e.preventDefault();
    save.mutate({ ...values });
  };

  return (
    <form className="settings-form" onSubmit={onSubmit}>
      {toggles.map((tg) => (
        <div key={tg.key} className="settings-field">
          <label className="settings-choice">
            <input
              type="checkbox"
              checked={values[tg.key]}
              aria-describedby={`${id}-${tg.key}`}
              onChange={(e) => {
                setValues({ ...values, [tg.key]: e.target.checked });
              }}
            />
            {t(tg.label)}
          </label>
          <small id={`${id}-${tg.key}`} className="muted">
            {t(tg.help)}
          </small>
        </div>
      ))}
      <SaveBar pending={save.isPending} saved={save.isSuccess} err={save.error} />
    </form>
  );
}

export default function PrivacyPage() {
  const { t } = useTranslation('settings');
  return <SettingsShell title={t('privacy.title')}>{(me) => <PrivacyForm me={me} />}</SettingsShell>;
}
