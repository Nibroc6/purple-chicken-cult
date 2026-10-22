// "Why am I seeing this?" (§5.3, §15.5). Owner: P0-W10 → P1-W09. The button
// opens a panel that loads GET /posts/{ref}/why lazily, once, through the loader
// the owning page provides (WhyLoaderContext; formats never fetch). Without a
// loader the button is shown but has nothing to open.
import { useContext, useEffect, useId, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Icon } from '../lib/Icon';
import { WhyLoaderContext, type WhyButtonProps, type WhyVM } from './types';
import { WhyPanel } from './WhyPanel';
import './integrity.css';

type State = { kind: 'idle' } | { kind: 'loading' } | { kind: 'ready'; why: WhyVM } | { kind: 'error' };

export function WhyButton({ post }: WhyButtonProps) {
  const { t } = useTranslation('integrity');
  const load = useContext(WhyLoaderContext);
  const [open, setOpen] = useState(false);
  const [state, setState] = useState<State>({ kind: 'idle' });
  const panelId = useId();
  const button = useRef<HTMLButtonElement>(null);
  const live = useRef(true);
  useEffect(() => {
    live.current = true;
    return () => {
      live.current = false;
    };
  }, []);

  const fetchWhy = () => {
    if (!load) return;
    setState({ kind: 'loading' });
    load(post).then(
      (why) => {
        if (live.current) setState({ kind: 'ready', why });
      },
      () => {
        if (live.current) setState({ kind: 'error' });
      },
    );
  };
  const toggle = () => {
    if (!load) return;
    if (!open && (state.kind === 'idle' || state.kind === 'error')) fetchWhy();
    setOpen(!open);
  };
  const close = () => {
    setOpen(false);
    button.current?.focus();
  };

  return (
    <span className="why-wrap">
      <button
        ref={button}
        type="button"
        className="integrity why"
        data-testid="integrity-why"
        data-post={post}
        aria-label={t('why.button')}
        title={t('why.button')}
        aria-expanded={load ? open : undefined}
        aria-controls={open ? panelId : undefined}
        aria-disabled={load ? undefined : true}
        onClick={toggle}
      >
        <Icon name="info" />
      </button>
      {open ? (
        <WhyPanel
          id={panelId}
          loading={state.kind === 'loading' || state.kind === 'idle'}
          failed={state.kind === 'error'}
          why={state.kind === 'ready' ? state.why : undefined}
          onClose={close}
          onRetry={fetchWhy}
        />
      ) : null}
    </span>
  );
}
