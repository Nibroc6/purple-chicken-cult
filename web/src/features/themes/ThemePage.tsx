// /settings/theme (ARCHITECTURE §15.2 A): pick a preset or adjust the design
// tokens (colors, corner radius, density, a font from the allowlist, text size
// from the floor up), see them live on a preview of the integrity elements
// with the contrast checks the server applies, and save. The default follows
// the app's light and dark palettes; a theme is one palette. The server
// validates every theme and rejects one whose integrity elements fall below
// WCAG AA (theme_contrast). Advanced mode adds the reader's own raw CSS
// (sheet.ts), behind a warning that it can change or hide anything, and the
// theme's own images for image(name). Owner: P1-W09.
import { useMutation, useQueryClient } from '@tanstack/react-query';
import { useEffect, useId, useRef, useState, type SyntheticEvent } from 'react';
import { useTranslation } from 'react-i18next';
import { ApiError, errorKey } from '../../api/errors';
import { Loading } from '../../app/Loading';
import { AudienceBadge, Chain, EditedMark, InviterLabel, ReportAction, TierHeader } from '../../integrity';
import { asRef } from '../../vm/types';
import { useUpload } from '../media';
import { clearTheme, saveTheme, themeKey, type ThemeView } from './api';
import { MAX_IMAGES, checkSheet, imageName, sheetImages, type SheetError } from './sheet';
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
import { useAppliedSheet, useAppliedTheme, useImageURLs, useStoredTheme, type SheetImage } from './useTheme';
import './themes.css';
import { InfoTip } from '../../lib/InfoTip';
import { Icon } from '../../lib/Icon';
import { IconButton } from '../../lib/IconButton';
import { SettingsSubNav } from '../../app/SettingsSubNav';

const themeCodes = new Set(['theme_contrast', 'theme_css', 'theme_font', 'theme_invalid', 'rate_limited']);

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
  if (err instanceof ApiError && cssLine(err) !== undefined) return 'themes:advanced.errors.theme_css';
  if (err instanceof ApiError && themeCodes.has(err.code)) return `themes:errors.${err.code}`;
  return errorKey(err);
}

// cssLine is the theme CSS line a rejection names (field css.<line>).
function cssLine(err: ApiError): number | undefined {
  const m = /^css\.(\d+)$/.exec(err.field ?? '');
  return m && err.code === 'theme_css' ? Number(m[1]) : undefined;
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
  const [css, setCSS] = useState(() => stored?.css ?? '');
  const [images, setImages] = useState<SheetImage[]>(() => (stored?.images ?? []).map((i) => ({ name: i.name, ref: i.ref })));
  const preview = useRef<HTMLDivElement>(null);
  useAppliedTheme(choice === 'default' ? undefined : tokens, preview);
  const names = new Set(images.map((i) => i.name));
  const sheetError = checkSheet(css, names);
  useAppliedSheet(choice !== 'default' && !sheetError ? css : undefined, images, 'preview');

  const save = useMutation({
    mutationFn: async () => {
      if (choice === 'default') {
        await clearTheme();
        return null;
      }
      const used = sheetImages(css);
      return saveTheme(
        tokens,
        css.trim(),
        images.filter((i) => used.has(i.name)),
      );
    },
    onSuccess: (v) => {
      qc.setQueryData(themeKey, v);
      if (v) {
        setCSS(v.css);
        setImages(v.images.map((i) => ({ name: i.name, ref: i.ref })));
      }
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

      <AdvancedFields
        disabled={!custom}
        css={css}
        images={images}
        error={sheetError}
        onCSS={(v) => {
          save.reset();
          setCSS(v);
        }}
        onImages={(v) => {
          save.reset();
          setImages(v);
        }}
      />

      {custom ? (
        <ul className="theme-checks" aria-label={t('check.legend')}>
          {results.map((r) => (
            <li key={r.id} data-testid="theme-check" data-check={r.id} data-ok={r.ok} data-failed={failed === r.id}>
              <Icon name={r.ok ? 'ok' : 'fail'} />
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
        <div ref={preview} className="theme-preview-canvas" data-testid="theme-preview" data-theme-preview="">
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
            <div className="engage-bar">
              <IconButton icon="like" label={t('preview.like')} data-part="like" aria-pressed={false} />
              <IconButton icon="save-on" label={t('preview.saved')} data-part="save" aria-pressed={true} />
            </div>
          </article>
          <p>
            <button type="button" className="primary">
              {t('preview.primary')}
            </button>
          </p>
        </div>
      </section>

      {err ? (
        <p role="alert" className="theme-error" data-testid="theme-error" data-code={err instanceof ApiError ? err.code : undefined}>
          {t(themeErrorKey(err) as 'errors:unknown', { check: failed ?? 'other', line: err instanceof ApiError ? (cssLine(err) ?? 0) : 0 })}
        </p>
      ) : null}
      {save.isSuccess ? (
        <p role="status" data-testid="theme-saved">
          {t('saved')}
        </p>
      ) : null}
      <div>
        <button type="submit" data-testid="theme-save" disabled={save.isPending || (custom && sheetError !== undefined)}>
          {save.isPending ? t('saving') : t('save')}
        </button>
      </div>
    </form>
  );
}

interface AdvancedProps {
  disabled: boolean;
  css: string;
  images: SheetImage[];
  error: SheetError | undefined;
  onCSS: (css: string) => void;
  onImages: (images: SheetImage[]) => void;
}

// AdvancedFields edits theme CSS and the theme's images (advanced mode).
function AdvancedFields({ disabled, css, images, error, onCSS, onImages }: AdvancedProps) {
  const { t } = useTranslation(['themes', 'errors']);
  const id = useId();
  const upload = useUpload();
  const urls = useImageURLs(images);
  const [name, setName] = useState('');
  const [file, setFile] = useState<File | null>(null);
  const [imageError, setImageError] = useState<string | undefined>();
  const fileInput = useRef<HTMLInputElement>(null);
  // Raw CSS can change or hide anything: it is editable once the reader says
  // they understand (a theme that already has CSS was acknowledged before).
  const [ack, setAck] = useState(css !== '');

  const add = async () => {
    const bad = !imageName.test(name)
      ? 'advanced.imageNameBad'
      : images.some((i) => i.name === name)
        ? 'advanced.imageNameTaken'
        : images.length >= MAX_IMAGES
          ? 'advanced.imagesFull'
          : !file
            ? 'advanced.imageFileMissing'
            : undefined;
    setImageError(bad);
    if (bad || !file) return;
    try {
      const m = await upload.upload(file, '');
      onImages([...images, { name, ref: m.ref }]);
      setName('');
      setFile(null);
      if (fileInput.current) fileInput.current.value = '';
    } catch (e) {
      setImageError(errorKey(e));
    }
  };

  return (
    <details className="theme-advanced" data-testid="theme-advanced" open={css !== '' || undefined}>
      <summary>{t('advanced.legend')}</summary>
      <fieldset disabled={disabled}>
        <p className="muted">{t('advanced.intro')}</p>
        <p className="theme-warning" role="note" data-testid="theme-css-warning">
          {t('advanced.warning')}
        </p>
        <label className="settings-choice">
          <input
            type="checkbox"
            data-testid="theme-css-ack"
            checked={ack}
            onChange={(e) => {
              setAck(e.target.checked);
            }}
          />
          {t('advanced.ack')}
        </label>
        <div className="settings-field">
          <label htmlFor={`${id}-css`}>{t('advanced.css')}</label>
          <textarea
            id={`${id}-css`}
            className="theme-css"
            data-testid="theme-css"
            dir="ltr"
            spellCheck={false}
            autoCapitalize="off"
            autoComplete="off"
            rows={12}
            disabled={!ack}
            value={css}
            aria-describedby={`${id}-css-status`}
            aria-invalid={error !== undefined}
            placeholder={t('advanced.placeholder')}
            onChange={(e) => {
              onCSS(e.target.value);
            }}
          />
          <small id={`${id}-css-status`} role="status" data-testid="theme-css-status" data-ok={error === undefined}>
            {error && error.line === 0 ? t('advanced.errors.whole') : null}
            {error && error.line > 0 ? t('advanced.errors.theme_css', { line: error.line }) : null}
          </small>
        </div>
        <p className="muted">{t('advanced.example')}</p>

        <fieldset className="theme-images">
          <legend>{t('advanced.images')}</legend>
          {upload.disabled ? <p className="muted">{t('advanced.imagesOff')}</p> : null}
          {images.length > 0 ? (
            <ul className="theme-image-list">
              {images.map((im) => (
                <li key={im.name} data-testid="theme-image" data-name={im.name}>
                  {urls.get(im.name) ? <img src={urls.get(im.name)} alt="" className="theme-image-thumb" /> : null}
                  <code>{t('advanced.imageUsage', { name: im.name })}</code>
                  <button
                    type="button"
                    data-testid="theme-image-remove"
                    onClick={() => {
                      onImages(images.filter((x) => x.name !== im.name));
                    }}
                  >
                    {t('advanced.imageRemove', { name: im.name })}
                  </button>
                </li>
              ))}
            </ul>
          ) : null}
          {upload.disabled ? null : (
            <div className="theme-image-add">
              <label htmlFor={`${id}-img-name`}>{t('advanced.imageName')}</label>
              <input
                id={`${id}-img-name`}
                data-testid="theme-image-name"
                dir="ltr"
                autoCapitalize="off"
                value={name}
                onChange={(e) => {
                  setName(e.target.value.trim().toLowerCase());
                }}
              />
              <label htmlFor={`${id}-img-file`}>{t('advanced.imageFile')}</label>
              <input
                id={`${id}-img-file`}
                ref={fileInput}
                type="file"
                accept="image/png,image/jpeg,image/gif,image/webp"
                data-testid="theme-image-file"
                onChange={(e) => {
                  setFile(e.target.files?.[0] ?? null);
                }}
              />
              <button type="button" data-testid="theme-image-add" disabled={upload.pending} onClick={() => void add()}>
                {upload.pending ? t('advanced.imageUploading') : t('advanced.imageAdd')}
              </button>
              {imageError ? (
                <p role="alert" className="theme-error" data-testid="theme-image-error">
                  {t(imageError as 'advanced.imageNameBad')}
                </p>
              ) : null}
            </div>
          )}
        </fieldset>
      </fieldset>
    </details>
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
      <SettingsSubNav />
      <p className="muted">
        {t('page.intro')}
        <InfoTip>{t('page.introTip')}</InfoTip>
      </p>
      {theme.isPending ? <Loading /> : <ThemeForm stored={theme.data ?? null} />}
    </section>
  );
}
