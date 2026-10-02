// Handshake audio contract (SCAFFOLD-1; ARCHITECTURE §7 "The handshake sound").
// P1-W13 implements it (features/handshake-audio/** except this file): one
// ~1.5 s buffer mixing the audible jingle (3-note motif + 8 notes × 3 bits = a
// 24-bit MAC) and near-ultrasonic FSK (32-bit MAC); Goertzel + FSK decoding on
// the device. No audio is ever recorded, uploaded or stored.

export interface PlayBits {
  ultra: Uint8Array; // 4 bytes
  jingle: Uint8Array; // 3 bytes
}

export interface DecodeReport {
  ultraBits?: Uint8Array;
  jingleBits?: Uint8Array;
  tUltraMs?: number;
  tJingleMs?: number;
  offsetMs?: number;
  motifLevelDb: number;
}

// AudioInput is the deterministic test seam (feeds pre-rendered PCM to the decoder).
export interface AudioInput {
  read(frames: number): Promise<Float32Array>;
}

export interface HandshakeAudio {
  play(bits: PlayBits, opts: { quiet: boolean }): Promise<{ startedAt: number }>;
  listen(opts: { timeoutMs: number; input?: AudioInput }): Promise<DecodeReport>;
}

// createHandshakeAudio is replaced by P1-W13's implementation.
export function createHandshakeAudio(_ctx: AudioContext): HandshakeAudio {
  return {
    play: () => Promise.reject(new Error('not_implemented')),
    listen: () => Promise.reject(new Error('not_implemented')),
  };
}
