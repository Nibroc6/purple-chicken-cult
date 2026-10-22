# The handshake sound

Sound design and codec notes for the in-person handshake (ARCHITECTURE §7 "The handshake
sound"; plan P1-W13). The code is `web/src/features/handshake-audio/`; the numbers below are
the constants in `layout.ts`.

## One sound, two layers

One 1.5 s buffer. Both layers start at t = 0 and play at the same time, so the handshake takes
no longer than either one alone, and a relay that passes one band but delays the other shows up
as an offset between them.

```
  ms     0    120  240       440 550 660     830 940 1050 1160 1270      1470 1500
jingle   [E5] [A5] [B5----]   [n0][n1][n2---] [n3][n4][n5] [n6] [n7------]
ultra    [P0][P1][P0][P1][d0][d1] … [d15]                                1400
```

### The jingle (audible, the sound logo)

- **Motif:** E5 → A5 → B5 (659, 880, 988 Hz), 100 / 100 / 160 ms. The same every time.
- **Tail:** 8 notes over a fixed rhythm (short short long, short short short short long:
  90 90 150 90 90 90 90 200 ms, 20 ms apart). Each note is one of 8 tones, so 3 bits per note
  and 24 bits in all (`jingle` = 3 bytes, most significant bit first).
- **Scale:** A major pentatonic over 1.4 octaves: C#6 E6 F#6 A6 B6 C#7 E7 F#7
  (1109–2960 Hz). A pentatonic scale has no semitones, so any of the 16.7 million tails is
  consonant and they all sound like the same song family. The whole range sits where cheap
  phone speakers are flat (above ~1 kHz, below 4 kHz).
- **Motif and tail never share a tone**, so a tail can never be mistaken for the motif.
- **Timbre:** pure sines with a 6 ms raised-cosine attack, a slow exponential decay
  (τ = 350 ms, a soft mallet) and a 12 ms release, so nothing clicks. Peak 0.5 of full scale.
- **Not translated** (ARCHITECTURE §15): the sound logo is the same in every locale.

### The ultrasonic layer

- 18.2–19.8 kHz, six tones 320 Hz apart. Mostly inaudible and short-range; calling apps
  filter it out, which is why it carries the most weight in the score.
- Continuous-phase FSK (no phase jumps, so no clicks) with 10 ms fades at both ends,
  70 ms symbols, peak 0.22 of full scale.
- **Preamble:** 4 symbols alternating the two outer tones (18.2 and 19.8 kHz), used only for
  sync. **Data:** 16 symbols on the four inner tones, 2 bits each, 32 bits in all (`ultra` =
  4 bytes, most significant bit first).
- A device whose output runs below ~41 kHz cannot produce it and plays no ultrasonic layer.
- **Quiet mode** plays only this layer.

The mix never exceeds 0.8 of full scale, so it never clips.

## Decoding

All on the device; samples live in memory for one `listen()` and are then dropped. No audio is
recorded, uploaded or stored; only bits and timings reach the server.

1. **Coarse grid.** Every 10 ms, a Hann-windowed Goertzel over 40 ms at every tone of both
   bands plus guard frequencies between them (the noise reference).
2. **Sync, per layer.** The motif (jingle) and the preamble (ultrasonic) are each scored by the
   share of their band's energy that sits in the expected tone at each slot. A layer is heard
   when the mean share is ≥ 0.4 and no slot is below 0.2, above a −85 dB floor.
3. **Search window.** The first layer heard anchors the search; the other one is looked for
   up to 400 ms either side. The decoder answers once the whole sound and that window have
   arrived (about 1.9 s after the sound starts).
4. **Fine sync.** ±30 ms in 1 ms steps: a matched filter (the note envelope) for the motif,
   full-symbol windows for the preamble.
5. **Bits.** Each tail note: the strongest of the 8 scale tones in its middle; each data
   symbol: the strongest of the 4 data tones in its middle.

The report (`DecodeReport`):

| Field | Meaning |
|---|---|
| `jingleBits`, `ultraBits` | Each layer that was heard (absent otherwise) |
| `tJingleMs`, `tUltraMs` | From the start of that layer, as heard, to the moment it was decoded (≈ 1.9 s; the server fails a layer over 3 s) |
| `offsetMs` | Jingle start − ultrasonic start, when both were heard (≈ 0; the sync bonus needs ≤ 100 ms) |
| `motifLevelDb` | The motif's level in dB of a full-scale sine, where it was heard or where it should have been (−120 when nothing was heard) |

**Volume reminder:** `volumeLow(report)` is true below −50 dB (`VOLUME_LOW_DB`): the motif was
faint or missing.

## Test seams

- `listen({ input })` reads any `AudioInput` instead of the microphone. An input with a
  `sampleRate` (a `PcmInput`) is decoded at its own rate.
- `pcmInput(pcm, rate)` and `wavInput(bytes)` feed PCM or a WAV; an empty chunk ends the listen.
- `soundWav(bits, { quiet })` renders a sound into a 16-bit WAV with silence around it. It is
  pure TypeScript (no Web Audio), so it runs in Node: this is the WAV INT-1's journey plays into
  Chromium's fake microphone (`--use-file-for-fake-audio-capture=<wav>`). `web/e2e/p1-w13.spec.ts`
  already does exactly that against the sound check page.

## Measured

Vitest (`codec.test.ts`), through a simulated phone speaker (2nd-order high-pass at 500 Hz,
1st-order low-pass at 5 kHz, about −12 dB at 19 kHz) with white noise at SNR 10 dB and a
random lead of 0.1–1 s: 150 of 150 jingles and 150 of 150 ultrasonic layers decoded at both
48 kHz and 44.1 kHz (acceptance: > 99 % and > 95 %). Noise and silence decode to nothing.
Chromium's fake microphone, end to end: both layers, and quiet mode.

## The sound check page

`/sound/check` (no sign-in needed, so an invitee can use it before joining): plays a sound with
throwaway random bits and shows its code, or listens for up to 15 s and shows what it heard,
including the volume reminder. Two people can compare codes across their phones. Nothing on the
page talks to the server.

## Hand-offs

- INT-1: the fake-audio journey can build its WAV with `soundWav` from the bits the claiming
  device plays (`bits` from `features/join`), and needs `--use-file-for-fake-audio-capture`.
- Linking `/sound/check` from the invite and join pages (P1-W02's files) or from settings
  (steward's `settingsNav`) is left to INT-1.
- The microphone tap is a `ScriptProcessorNode` (deprecated, but in every browser); an
  `AudioWorklet` would need its own module URL under the CSP.
