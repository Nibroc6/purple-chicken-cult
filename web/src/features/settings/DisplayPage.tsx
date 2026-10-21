// Display settings: name, layout, density, accessibility and feed order (§10.7,
// §15.1). Owner: P0-W11 → P1-W10.
import { useId, useState, type SyntheticEvent } from 'react';
import { useTranslation } from 'react-i18next';
import type { MeView } from '../../app/session';
import { applyA11y } from '../../lib/a11y';
import { SaveBar, SettingsShell, useSaveSettings } from './shared';
import {
  contrasts,
  densities,
  formats,
  fromUIPrefs,
  presetChoice,
  presets,
  textScales,
  toA11y,
  toUIPrefs,
  type UIPrefs,
} from './vm';

const formatLabel = { cards: 'display.format.cards', compact: 'display.format.compact', explorer: 'display.format.explorer' } as const;
const densityLabel = { comfortable: 'display.density.comfortable', compact: 'display.density.compact' } as const;
const contrastLabel = { normal: 'display.a11y.contrastNormal', high: 'display.a11y.contrastHigh' } as const;
const textLabel = {
  small: 'display.a11y.textSmall',
  normal: 'display.a11y.textNormal',
  large: 'display.a11y.textLarge',
  xlarge: 'display.a11y.textXlarge',
} as const;
const presetLabel = { latest: 'display.preset.latest', balanced: 'display.preset.balanced' } as const;

function DisplayForm({ me }: { me: MeView }) {
  const { t } = useTranslation('settings');
  const id = useId();
  const [name, setName] = useState(me.display_name);
  const [prefs, setPrefs] = useState<UIPrefs>(() => toUIPrefs(me.settings.ui_prefs));
  const [preset, setPreset] = useState(() => presetChoice(me.settings));
  const save = useSaveSettings();
  const setA11y = (patch: Partial<UIPrefs['a11y']>) => {
    setPrefs((p) => ({ ...p, a11y: { ...p.a11y, ...patch } }));
  };

  const onSubmit = (e: SyntheticEvent) => {
    e.preventDefault();
    save.mutate(
      { display_name: name, ui_prefs: fromUIPrefs(prefs), rank_preset: preset },
      {
        onSuccess: () => {
          applyA11y(toA11y(prefs));
        },
      },
    );
  };

  return (
    <form className="settings-form" onSubmit={onSubmit}>
      <div className="settings-field">
        <label htmlFor={`${id}-name`}>{t('display.name.label')}</label>
        <input
          id={`${id}-name`}
          value={name}
          maxLength={50}
          aria-describedby={`${id}-name-help`}
          onChange={(e) => {
            setName(e.target.value);
          }}
        />
        <small id={`${id}-name-help`} className="muted">
          {t('display.name.help')}
        </small>
      </div>

      <fieldset>
        <legend>{t('display.format.legend')}</legend>
        {formats.map((f) => (
          <label key={f} className="settings-choice">
            <input
              type="radio"
              name={`${id}-format`}
              value={f}
              checked={prefs.format === f}
              onChange={() => {
                setPrefs((p) => ({ ...p, format: f }));
              }}
            />
            {t(formatLabel[f])}
          </label>
        ))}
      </fieldset>

      <fieldset>
        <legend>{t('display.density.legend')}</legend>
        {densities.map((d) => (
          <label key={d} className="settings-choice">
            <input
              type="radio"
              name={`${id}-density`}
              value={d}
              checked={prefs.density === d}
              onChange={() => {
                setPrefs((p) => ({ ...p, density: d }));
              }}
            />
            {t(densityLabel[d])}
          </label>
        ))}
      </fieldset>

      <fieldset>
        <legend>{t('display.a11y.legend')}</legend>
        <div className="settings-field">
          <label htmlFor={`${id}-contrast`}>{t('display.a11y.contrast')}</label>
          <select
            id={`${id}-contrast`}
            value={prefs.a11y.contrast}
            onChange={(e) => {
              const v = contrasts.find((c) => c === e.target.value);
              if (v) setA11y({ contrast: v });
            }}
          >
            {contrasts.map((c) => (
              <option key={c} value={c}>
                {t(contrastLabel[c])}
              </option>
            ))}
          </select>
        </div>
        <div className="settings-field">
          <label htmlFor={`${id}-text`}>{t('display.a11y.textScale')}</label>
          <select
            id={`${id}-text`}
            value={prefs.a11y.textScale}
            onChange={(e) => {
              const v = textScales.find((s) => s === e.target.value);
              if (v) setA11y({ textScale: v });
            }}
          >
            {textScales.map((s) => (
              <option key={s} value={s}>
                {t(textLabel[s])}
              </option>
            ))}
          </select>
        </div>
        <label className="settings-choice">
          <input
            type="checkbox"
            checked={prefs.a11y.reducedMotion}
            onChange={(e) => {
              setA11y({ reducedMotion: e.target.checked });
            }}
          />
          {t('display.a11y.reducedMotion')}
        </label>
        <label className="settings-choice">
          <input
            type="checkbox"
            checked={prefs.a11y.dyslexiaFont}
            onChange={(e) => {
              setA11y({ dyslexiaFont: e.target.checked });
            }}
          />
          {t('display.a11y.dyslexiaFont')}
        </label>
      </fieldset>

      <fieldset>
        <legend>{t('display.preset.legend')}</legend>
        {presets.map((p) => (
          <label key={p} className="settings-choice">
            <input
              type="radio"
              name={`${id}-preset`}
              value={p}
              checked={preset === p}
              onChange={() => {
                setPreset(p);
              }}
            />
            {t(presetLabel[p])}
          </label>
        ))}
        <small className="muted">{t('display.preset.help')}</small>
      </fieldset>

      <SaveBar pending={save.isPending} saved={save.isSuccess} err={save.error} />
    </form>
  );
}

export default function DisplayPage() {
  const { t } = useTranslation('settings');
  return <SettingsShell title={t('display.title')}>{(me) => <DisplayForm me={me} />}</SettingsShell>;
}
