// Sound check: plays a handshake sound with random bits and shows its code, or
// listens for one and shows what it heard, so two people can check their
// devices before (or after a failed) in-person handshake. The codes are
// throwaway: nothing here talks to the server. Owner: P1-W13.
import { useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import type { DecodeReport, HandshakeAudio } from './index';
import { createHandshakeAudio } from './createHandshakeAudio';
import { volumeLow } from './decoder';
import { InfoTip } from '../../lib/InfoTip';

const LISTEN_MS = 15_000;

const hex = (b: Uint8Array) => Array.from(b, (v) => v.toString(16).padStart(2, '0')).join('');

type Phase = 'idle' | 'playing' | 'listening';

export default function SoundCheckPage() {
  const { t } = useTranslation('handshake');
  const audio = useRef<HandshakeAudio | undefined>(undefined);
  const [phase, setPhase] = useState<Phase>('idle');
  const [quiet, setQuiet] = useState(false);
  const [played, setPlayed] = useState<string | undefined>();
  const [heard, setHeard] = useState<DecodeReport | undefined>();
  const [err, setErr] = useState<'noMic' | 'noAudio' | undefined>();
  // The AudioContext is created on the first click (autoplay rules).
  const device = () => (audio.current ??= createHandshakeAudio(new AudioContext()));

  const play = async () => {
    setErr(undefined);
    setHeard(undefined);
    const bits = {
      ultra: crypto.getRandomValues(new Uint8Array(4)),
      jingle: crypto.getRandomValues(new Uint8Array(3)),
    };
    setPlayed(quiet ? hex(bits.ultra) : `${hex(bits.jingle)} · ${hex(bits.ultra)}`);
    setPhase('playing');
    try {
      await device().play(bits, { quiet });
    } catch {
      setErr('noAudio');
    }
    setPhase('idle');
  };

  const listen = async () => {
    setErr(undefined);
    setPlayed(undefined);
    setHeard(undefined);
    setPhase('listening');
    try {
      setHeard(await device().listen({ timeoutMs: LISTEN_MS }));
    } catch {
      setErr('noMic');
    }
    setPhase('idle');
  };

  const busy = phase !== 'idle';
  const nothing = heard && !heard.jingleBits && !heard.ultraBits;
  return (
    <main className="sound-check">
      <h1>{t('check.title')}</h1>
      <p>
        {t('check.body')}
        <InfoTip>{t('check.bodyTip')}</InfoTip>
      </p>
      <p>
        <label>
          <input
            type="checkbox"
            data-testid="sound-check-quiet"
            checked={quiet}
            disabled={busy}
            onChange={(e) => {
              setQuiet(e.target.checked);
            }}
          />{' '}
          {t('check.playQuiet')}
        </label>
      </p>
      <p>
        <button type="button" className="primary" data-testid="sound-check-play" disabled={busy} onClick={() => void play()}>
          {phase === 'playing' ? t('check.playing') : t('check.play')}
        </button>{' '}
        <button type="button" data-testid="sound-check-listen" disabled={busy} onClick={() => void listen()}>
          {t('check.listen')}
        </button>
      </p>
      <div role="status" aria-live="polite">
        {phase === 'listening' ? <p data-testid="sound-check-listening">{t('check.listening')}</p> : null}
        {played && phase === 'idle' && !err ? (
          <p data-testid="sound-check-played" data-code={played}>
            {t('check.played', { code: played })}
          </p>
        ) : null}
        {heard && !nothing ? (
          <>
            {heard.jingleBits ? (
              <p data-testid="sound-check-jingle" data-code={hex(heard.jingleBits)}>
                {t('check.heardJingle', { code: hex(heard.jingleBits) })}
              </p>
            ) : (
              <p data-testid="sound-check-no-jingle">{t('check.missedJingle')}</p>
            )}
            {heard.ultraBits ? (
              <p data-testid="sound-check-ultra" data-code={hex(heard.ultraBits)}>
                {t('check.heardUltra', { code: hex(heard.ultraBits) })}
              </p>
            ) : (
              <p data-testid="sound-check-no-ultra">{t('check.missedUltra')}</p>
            )}
          </>
        ) : null}
        {heard && !nothing && volumeLow(heard) && heard.jingleBits ? (
          <p data-testid="sound-check-volume">{t('check.volumeLow')}</p>
        ) : null}
        {nothing ? <p data-testid="sound-check-nothing">{t('check.nothing')}</p> : null}
      </div>
      {err ? (
        <p role="alert" data-testid="sound-check-error">
          {t(err === 'noMic' ? 'check.noMic' : 'check.noAudio')}
        </p>
      ) : null}
    </main>
  );
}
