// Chain (§4.5, §15.5). Owner: P0-W10 → P1-W09. "you → @first → 2 others →
// @author": the first hop and the author always, middle hops only as the API
// names them (both endpoints of every named edge opted in), the rest collapsed
// into a count. Handles only, never display names. Drawn as a path of chips:
// the reader's own tie (friend or acquaintance) on the first hop, and the
// number of steps at the end. Given the post, the chain opens the "how you're
// connected" modal (?via=<post>).
import type { ReactNode } from 'react';
import { useTranslation } from 'react-i18next';
import { useInRouterContext } from 'react-router';
import { useModalParam } from '../app/Modal';
import type { ChainVM } from '../vm/types';
import type { ChainProps } from './types';
import './integrity.css';

// viaParam is the search parameter of the connection modal (?via=<post ref>).
export const viaParam = 'via';

export function Chain({ chain, author, post }: ChainProps) {
  const { t } = useTranslation('integrity');
  const inRouter = useInRouterContext();
  const line = <ChainLine chain={chain} authorText={t('chain.handle', { handle: author.handle })} testId="integrity-chain" />;
  if (!post || !inRouter || chain.dist === 0) return line;
  return <OpenVia post={post}>{line}</OpenVia>;
}

function OpenVia({ post, children }: { post: string; children: ReactNode }) {
  const { t } = useTranslation('integrity');
  const [, open] = useModalParam(viaParam);
  return (
    <button
      type="button"
      className="integrity chain-open"
      data-testid="integrity-chain-open"
      title={t('via.open')}
      onClick={() => {
        open(post);
      }}
    >
      {children}
    </button>
  );
}

export interface ChainStep {
  key: string;
  text: string;
  kind: 'you' | 'hop' | 'hidden' | 'author';
  // tie: the reader's own tie to this hop (only the first hop is the reader's).
  tie?: 'friend' | 'acquaintance';
}

// chainSteps lists the chain's steps from the reader to the author.
export function chainSteps(chain: ChainVM, authorText: string, t: (k: string, o?: Record<string, unknown>) => string): ChainStep[] {
  if (chain.dist === 0) return [{ key: 'self', text: t('chain.self'), kind: 'you' }];
  const tie = chain.firstHopPlus ? 'friend' : 'acquaintance';
  const steps: ChainStep[] = [{ key: 'you', text: t('chain.you'), kind: 'you' }];
  if (chain.firstHop && chain.dist > 1) steps.push({ key: `h:${chain.firstHop.ref}`, text: t('chain.handle', { handle: chain.firstHop.handle }), kind: 'hop', tie });
  for (const h of chain.middle) steps.push({ key: `m:${h.ref}`, text: t('chain.handle', { handle: h.handle }), kind: 'hop' });
  if (chain.hiddenMiddle > 0) steps.push({ key: 'hidden', text: t('chain.hidden', { count: chain.hiddenMiddle }), kind: 'hidden' });
  steps.push({ key: 'author', text: authorText, kind: 'author', ...(chain.dist === 1 ? { tie } : {}) });
  return steps;
}

// ChainLine renders a chain ending in authorText (the "why" panel names the
// author generically: its view has no handle).
export function ChainLine({ chain, authorText, testId }: { chain: ChainVM; authorText: string; testId: string }) {
  const { t } = useTranslation('integrity');
  const tr = t as unknown as (k: string, o?: Record<string, unknown>) => string;
  const steps = chainSteps(chain, authorText, tr);
  const sep = t('chain.sep');
  const stepsText = chain.dist > 0 ? t('chain.steps', { count: chain.dist }) : '';
  return (
    <span
      className="integrity chain"
      role="group"
      data-testid={testId}
      data-dist={chain.dist}
      data-first-hop-plus={chain.firstHopPlus ? 'true' : undefined}
      aria-label={stepsText ? `${t('chain.label')}, ${stepsText}` : t('chain.label')}
    >
      <span className="chain-path">
        {steps.map((s, i) => (
          <span key={s.key} className="chain-step">
            {i > 0 ? <span className="chain-sep">{` ${sep} `}</span> : null}
            <span className="chain-node" data-kind={s.kind} data-tie={s.tie}>
              {s.text}
              {s.tie ? (
                <>
                  {' '}
                  <span className="chain-tie">{t('chain.tie', { tie: s.tie })}</span>
                </>
              ) : null}
            </span>
          </span>
        ))}
      </span>
      {stepsText ? ' ' : null}
      {stepsText ? (
        <span className="chain-steps" data-testid={`${testId}-steps`}>
          {stepsText}
        </span>
      ) : null}
    </span>
  );
}
