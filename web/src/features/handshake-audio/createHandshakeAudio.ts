// createHandshakeAudio (features/handshake-audio contract). Owner: P1-W13.
// Scaffold placeholder: both directions reject until the codec lands.
import type { HandshakeAudio } from './index';

export function createHandshakeAudio(_ctx: AudioContext): HandshakeAudio {
  return {
    play: () => Promise.reject(new Error('not_implemented')),
    listen: () => Promise.reject(new Error('not_implemented')),
  };
}
