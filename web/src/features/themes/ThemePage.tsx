// /settings/theme (ARCHITECTURE §15.2 A): pick a preset or adjust the design
// tokens (colors, corner radius, density, a font from the allowlist, text size
// from the floor up), see them live on a preview of the integrity elements
// with the contrast checks the server applies, and save. The default follows
// the app's light and dark palettes; a theme is one palette. The server
// validates every theme and rejects one whose integrity elements fall below
// WCAG AA (theme_contrast). Owner: P1-W09.
import { useMutation, useQueryClient } from '@tanstack/react-query';
import { useEffect, useId, useRef, useState, type SyntheticEvent } from 'react';
import { useTranslation } from 'react-i18next';
import { ApiError, errorKey } from '../../api/errors';
import { Loading } from '../../app/Loading';
import { AudienceBadge, Chain, EditedMark, InviterLabel, ReportAction, TierHeader } from '../../integrity';
import { asRef } from '../../vm/types';
import { clearTheme, saveTheme, themeKey, type ThemeView } from './api';
import {
  FONT_SIZE,
  RADIUS,
  checks,
  colorNames,
  densities,
  fonts,
  presetOf,
  presets,
  type ColorName,
  type ThemeTokens,
} from './theme';
import { useAppliedTheme, useStoredTheme } from './useTheme';
import './themes.css';

const themeCodes = new Set(['theme_contrast', 'theme_font', 'theme_invalid', 'rate_limited']);

// The check a theme_contrast field belongs to.
const fieldCheck: Record<string, string> = {
  'colors.integrity_text': 'integrity',
  'colors.text': 'text',
  'colors.muted': 'muted',
  'colors.accent': 'links',
};

// themeErrorKey is themes:errors.<code> for the codes this page explains.
// i18n-dynamic: themes:errors.
export function themeErrorKey(err: unknown): string {
  if (err instanceof ApiError && themeCodes.has(err.code)) return `themes:errors.${err.code}`;
  return errorKey(err);
}

type Choice = 'default' | 'custom' | (string & {});

const defaultTokens = (presets[0]?.tokens ?? { colors: {} }) as ThemeTokens;
const CUSTOM: Choice = 'custom';
const choices: readonly Choice[] = ['default', ...presets.map((p) => p.id)];
const swatchColors: readonly ColorName[] = ['bg', 'surface', 'text', 'accent', 'integrity_bg'];

function Swatch({ color }: { color: string }) {
  const ref = useRef<HTMLSpanElement>(null);
  useEffect(() => {
    ref.current?.style.setProperty('--swatch', color);
  }, [color]);
  return <span ref={ref} className="theme-swatch" aria-hidden="true" />;
}

// i18n-dynamic: themes:preset.
// i18n-dynamic: themes:color.
// i18n-dynamic: themes:font.
// i18n-dynamic: themes:density.
// i18n-dynamic: themes:check.
function ThemeForm({ stored }: { stored: ThemeView | null }) {
  const { t } = useTranslation(['themes', 'errors']);
  const id = useId();
  const qc = useQueryClient();
  const initial = stored?.tokens;
  const [choice, setChoice] = useState<Choice>(() => (initial ? (presetOf(initial) ?? 'custom') : 'default'));
  const [tokens, setTokens] = useState<ThemeTokens>(() => initial ?? defaultTokens);
  const preview = useRef<HTMLDivElement>(null);
  useAppliedTheme(choice === 'default' ? undefined : tokens, preview);

  const save = useMutation({
    mutationFn: async () => {
      if (choice === 'default') {
        await clearTheme();
        return null;
      }
      return saveTheme(tokens);
    },
    onSuccess: (v) => {
      qc.setQueryData(themeKey, v);
    },
  });

  const edit = (patch: Partial<ThemeTokens>) => {
    save.reset();
    setChoice('custom');
    setTokens((cur) => ({ ...cur, ...patch }));
  };
  const setColor = (c: ColorName, v: string) => {
    edit({ colors: { ...tokens.colors, [c]: v } });
  };
  const pick = (c: Choice) => {
    save.reset();
    setChoice(c);
    const p = presets.find((x) => x.id === c);
    if (p) setTokens(p.tokens);
  };

  const onSubmit = (e: SyntheticEvent) => {
    e.preventDefault();
    save.mutate();
  };

  const err = save.error;
  const failed = err instanceof ApiError && err.code === 'theme_contrast' ? fieldCheck[err.field ?? ''] : undefined;
  const results = checks(tokens.colors);
  const custom = choice !== 'default';

  return (
    <form className="settings-form theme-form" data-testid="theme-form" data-choice={choice} onSubmit={onSubmit}>
      <fieldset>
        <legend>{t('preset.legend')}</legend>
        {[...choices, ...(choice === 'custom' ? [CUSTOM] : [])].map((c) => {
          const p = presets.find((x) => x.id === c);
          return (
            <label key={c} className="settings-choice theme-preset">
              <input
                type="radio"
                name={`${id}-preset`}
                value={c}
                data-testid="theme-preset"
                data-preset={c}
                checked={choice === c}
                onChange={() => {
                  pick(c);
                }}
              />
              {t(`preset.${c}` as 'preset.default')}
              {p ? (
                <span className="theme-swatches">
                  {swatchColors.map((k) => (
                    <Swatch key={k} color={p.tokens.colors[k]} />
                  ))}
                </span>
              ) : null}
            </label>
          );
        })}
      </fieldset>

      <fieldset disabled={!custom}>
        <legend>{t('colors.legend')}</legend>
        <div className="theme-colors">
          {colorNames.map((c) => (
            <label key={c} className="theme-color">
              <input
                type="color"
                data-testid={`theme-color-${c}`}
                value={tokens.colors[c]}
                onChange={(e) => {
                  setColor(c, e.target.value);
                }}
              />
              {t(`color.${c}` as 'color.bg')}
            </label>
          ))}
        </div>
      </fieldset>

      <fieldset disabled={!custom}>
        <legend>{t('shape.legend')}</legend>
        <div className="settings-field">
          <label htmlFor={`${id}-radius`}>{t('shape.radius', { px: tokens.radius ?? 12 })}</label>
          <input
            id={`${id}-radius`}
            type="range"
            data-testid="theme-radius"
            min={RADIUS.min}
            max={RADIUS.max}
            value={tokens.radius ?? 12}
            onChange={(e) => {
              edit({ radius: Number(e.target.value) });
            }}
          />
        </div>
        <div className="settings-field">
          <label htmlFor={`${id}-density`}>{t('shape.density')}</label>
          <select
            id={`${id}-density`}
            data-testid="theme-density"
            value={tokens.density ?? 'comfortable'}
            onChange={(e) => {
              edit({ density: e.target.value as ThemeTokens['density'] });
            }}
          >
            {densities.map((d) => (
              <option key={d} value={d}>
                {t(`density.${d}` as 'density.compact')}
              </option>
            ))}
          </select>
        </div>
      </fieldset>

      <fieldset disabled={!custom}>
        <legend>{t('type.legend')}</legend>
        <div className="settings-field">
          <label htmlFor={`${id}-font`}>{t('type.font')}</label>
          <select
            id={`${id}-font`}
            data-testid="theme-font"
            value={tokens.font ?? 'system'}
            onChange={(e) => {
              edit({ font: e.target.value as ThemeTokens['font'] });
            }}
          >
            {fonts.map((f) => (
              <option key={f} value={f}>
                {t(`font.${f}` as 'font.system')}
              </option>
            ))}
          </select>
        </div>
        <div className="settings-field">
          <label htmlFor={`${id}-size`}>{t('type.size', { px: tokens.font_size ?? 16 })}</label>
          <input
            id={`${id}-size`}
            type="range"
            data-testid="theme-font-size"
            min={FONT_SIZE.min}
            max={FONT_SIZE.max}
            value={tokens.font_size ?? 16}
            aria-describedby={`${id}-size-help`}
            onChange={(e) => {
              edit({ font_size: Number(e.target.value) });
            }}
          />
          <small id={`${id}-size-help`} className="muted">
            {t('type.sizeHelp')}
          </small>
        </div>
      </fieldset>

      {custom ? (
        <ul className="theme-checks" aria-label={t('check.legend')}>
          {results.map((r) => (
            <li key={r.id} data-testid="theme-check" data-check={r.id} data-ok={r.ok} data-failed={failed === r.id}>
              {t(r.ok ? 'check.ok' : 'check.low', {
                what: t(`check.${r.id}` as 'check.text'),
                ratio: Math.floor(r.ratio * 10) / 10,
                min: r.min,
              })}
            </li>
          ))}
        </ul>
      ) : null}

      <section className="theme-preview" aria-label={t('preview.label')}>
        <div ref={preview} className="theme-preview-canvas" data-testid="theme-preview">
          <TierHeader tier={1} kind="start" />
          <article className="post">
            <header className="post-author">
              <InviterLabel inviter={{ kind: 'user', handle: t('preview.inviter') }} invitee={asRef('preview')} onReport={noop} />
              <Chain chain={{ dist: 2, firstHop: { ref: asRef('preview-hop'), handle: t('preview.hop') }, middle: [], hiddenMiddle: 0, firstHopPlus: false }} author={{ ref: asRef('preview'), handle: t('preview.author') }} />
            </header>
            <p>{t('preview.body')}</p>
            <p className="muted">{t('preview.muted')}</p>
            <p>
              <a href="#preview" onClick={stop}>
                {t('preview.link')}
              </a>
            </p>
            <footer className="post-meta">
              <AudienceBadge audience="d2" />
              <EditedMark editedAt={new Date(0)} />
              <ReportAction target={{ kind: 'post', ref: asRef('preview') }} onReport={noop} />
            </footer>
          </article>
        </div>
      </section>

      {err ? (
        <p role="alert" className="theme-error" data-testid="theme-error" data-code={err instanceof ApiError ? err.code : undefined}>
          {t(themeErrorKey(err) as 'errors:unknown', { check: failed ?? 'other' })}
        </p>
      ) : null}
      {save.isSuccess ? (
        <p role="status" data-testid="theme-saved">
          {t('saved')}
        </p>
      ) : null}
      <div>
        <button type="submit" data-testid="theme-save" disabled={save.isPending}>
          {save.isPending ? t('saving') : t('save')}
        </button>
      </div>
    </form>
  );
}

function noop() {}

function stop(e: SyntheticEvent) {
  e.preventDefault();
}

export default function ThemePage() {
  const { t } = useTranslation('themes');
  const theme = useStoredTheme();
  return (
    <section className="page theme-page" data-testid="theme-page">
      <h1>{t('page.title')}</h1>
      <p className="muted">{t('page.intro')}</p>
      {theme.isPending ? <Loading /> : <ThemeForm stored={theme.data ?? null} />}
    </section>
  );
}
