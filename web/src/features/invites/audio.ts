// Access to the handshake sound (features/handshake-audio, P1-W13's codec)
// for both devices. One AudioContext per page, created on first use (from a
// click, as browsers require).
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

let shared: HandshakeAudio | undefined;

export function handshakeAudio(): HandshakeAudio {
  const seam = (globalThis as Seams).pccHandshakeAudio;
  if (seam) return seam();
  shared ??= createHandshakeAudio(new AudioContext());
  return shared;
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
