// Access to the handshake sound (features/handshake-audio, P1-W13's codec)
// for both devices. One AudioContext per page, created on first use. Call
// handshakeAudio() synchronously in the click handler, before any await:
// Safari and iOS only let an AudioContext start inside the gesture itself, so
// it is resumed (and unlocked with a silent buffer) right there.
//
// Test seam (plan §7, the AudioInput seam of the HandshakeAudio contract): a
// page may define globalThis.pccHandshakeAudio (a HandshakeAudio factory) and
// globalThis.pccAudioInput (the PCM the listener reads instead of the
// microphone). Playwright installs them with an init script; nothing in the
// app sets them. Owner: P1-W02.
import { createHandshakeAudio, type AudioInput, type DecodeReport, type HandshakeAudio } from '../handshake-audio';
import { b64url } from './handshake';

interface Seams {
  pccHandshakeAudio?: () => HandshakeAudio;
  pccAudioInput?: AudioInput;
}

let shared: { ctx: AudioContext; audio: HandshakeAudio } | undefined;

export function handshakeAudio(): HandshakeAudio {
  const seam = (globalThis as Seams).pccHandshakeAudio;
  if (seam) return seam();
  if (!shared) {
    const ctx = new AudioContext();
    shared = { ctx, audio: createHandshakeAudio(ctx) };
  }
  unlock(shared.ctx);
  return shared.audio;
}

// unlock starts ctx from within the user gesture: resume() plus a one-sample
// silent buffer, which is what iOS Safari needs to let later playback through.
export function unlock(ctx: AudioContext): void {
  if (ctx.state === 'running') return;
  void ctx.resume().catch(() => undefined);
  try {
    const src = ctx.createBufferSource();
    src.buffer = ctx.createBuffer(1, 1, ctx.sampleRate);
    src.connect(ctx.destination);
    src.start();
  } catch {
    // play() reports a context that never starts.
  }
}

export function audioInput(): AudioInput | undefined {
  return (globalThis as Seams).pccAudioInput;
}

// reportBody turns a decode report into the report request (bits base64url,
// timings in whole milliseconds).
export function reportBody(r: DecodeReport, kmacCommit: string) {
  const ms = (v: number | undefined) => (v === undefined || !Number.isFinite(v) ? undefined : Math.round(v));
  const tUltra = ms(r.tUltraMs);
  const tJingle = ms(r.tJingleMs);
  const offset = ms(r.offsetMs);
  return {
    kmac_commit: kmacCommit,
    ...(r.ultraBits ? { ultra_bits: b64url(r.ultraBits) } : {}),
    ...(r.jingleBits ? { jingle_bits: b64url(r.jingleBits) } : {}),
    ...(tUltra !== undefined ? { t_ultra_ms: tUltra } : {}),
    ...(tJingle !== undefined ? { t_jingle_ms: tJingle } : {}),
    ...(offset !== undefined ? { offset_ms: offset } : {}),
    ...(Number.isFinite(r.motifLevelDb) ? { motif_level_db: r.motifLevelDb } : {}),
  };
}
