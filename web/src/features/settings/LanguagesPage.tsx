// Language settings: UI locale, the languages the user reads (first = default
// post language), translation target (∈ languages) and autotranslate (§15.4).
// Owner: P0-W11 → P1-W10.
import { useId, useState, type SyntheticEvent } from 'react';
import { useTranslation } from 'react-i18next';
import type { MeView } from '../../app/session';
import { SaveBar, SettingsShell, useSaveSettings } from './shared';
import { canonicalTag, translateOptions } from './vm';

function languageName(tag: string, locale: string): string {
  try {
    return new Intl.DisplayNames([locale], { type: 'language' }).of(tag) ?? tag;
  } catch {
    return tag;
  }
}

function LanguagesForm({ me }: { me: MeView }) {
  const { t, i18n } = useTranslation('settings');
  const id = useId();
  const [locale, setLocale] = useState(me.settings.ui_locale);
  const [langs, setLangs] = useState<string[]>(me.settings.languages);
  const [draft, setDraft] = useState('');
  const [translateTo, setTranslateTo] = useState(me.settings.translate_to ?? '');
  const [auto, setAuto] = useState(me.settings.autotranslate);
  const save = useSaveSettings();
  const options = translateOptions(langs);
  const noTarget = '';
  const noTargetLabel = t('languages.translateTo.none');
  const target = options.includes(translateTo) ? translateTo : noTarget;

  const add = () => {
    const tag = canonicalTag(draft) ?? draft.trim();
    if (tag !== '' && !langs.includes(tag)) setLangs([...langs, tag]);
    setDraft('');
  };

  const onSubmit = (e: SyntheticEvent) => {
    e.preventDefault();
    save.mutate({ ui_locale: locale.trim(), languages: langs, translate_to: target, autotranslate: auto });
  };

  return (
    <form className="settings-form" onSubmit={onSubmit}>
      <div className="settings-field">
        <label htmlFor={`${id}-locale`}>{t('languages.uiLocale.label')}</label>
        <input
          id={`${id}-locale`}
          value={locale}
          aria-describedby={`${id}-locale-help`}
          autoCapitalize="off"
          spellCheck={false}
          onChange={(e) => {
            setLocale(e.target.value);
          }}
        />
        <small id={`${id}-locale-help`} className="muted">
          {t('languages.uiLocale.help')}
        </small>
      </div>

      <fieldset>
        <legend>{t('languages.list.legend')}</legend>
        <p className="muted">{t('languages.list.help')}</p>
        <ul className="settings-langs">
          {langs.map((l) => (
            <li key={l}>
              <span>
                {languageName(l, i18n.language)} <span className="muted">({l})</span>
              </span>
              <button
                type="button"
                disabled={langs.length <= 1}
                onClick={() => {
                  setLangs(langs.filter((x) => x !== l));
                }}
              >
                {t('languages.list.remove', { lang: l })}
              </button>
            </li>
          ))}
        </ul>
        <div className="settings-actions">
          <label htmlFor={`${id}-new`}>{t('languages.list.newLabel')}</label>
          <input
            id={`${id}-new`}
            value={draft}
            autoCapitalize="off"
            spellCheck={false}
            onChange={(e) => {
              setDraft(e.target.value);
            }}
            onKeyDown={(e) => {
              if (e.key === 'Enter') {
                e.preventDefault();
                add();
              }
            }}
          />
          <button type="button" onClick={add} disabled={draft.trim() === ''}>
            {t('languages.list.add')}
          </button>
        </div>
      </fieldset>

      <div className="settings-field">
        <label htmlFor={`${id}-translate`}>{t('languages.translateTo.label')}</label>
        <select
          id={`${id}-translate`}
          value={target}
          onChange={(e) => {
            setTranslateTo(e.target.value);
          }}
        >
          <option value={noTarget}>{noTargetLabel}</option>
          {options.map((l) => (
            <option key={l} value={l}>
              {languageName(l, i18n.language)}
            </option>
          ))}
        </select>
      </div>
      <label className="settings-choice">
        <input
          type="checkbox"
          checked={auto}
          onChange={(e) => {
            setAuto(e.target.checked);
          }}
        />
        {t('languages.autotranslate')}
      </label>

      <SaveBar pending={save.isPending} saved={save.isSuccess} err={save.error} />
    </form>
  );
}

export default function LanguagesPage() {
  const { t } = useTranslation('settings');
  return <SettingsShell title={t('languages.title')}>{(me) => <LanguagesForm me={me} />}</SettingsShell>;
}
