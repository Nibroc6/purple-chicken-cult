-- Phase 1 (SCAFFOLD-1): two-layer handshake (QR frames + audio), handshake score.
ALTER TABLE handshake_sessions
  ADD COLUMN mode            smallint NOT NULL DEFAULT 0,   -- 0 qr (P0), 1 qr+audio, 2 dual_qr (accessibility)
  ADD COLUMN quiet           boolean  NOT NULL DEFAULT false,
  ADD COLUMN frame_key       bytea,                         -- given to inviter device to render 250ms frames
  ADD COLUMN inviter_epub    bytea,                         -- E_i (P-256, uncompressed)
  ADD COLUMN invitee_epub    bytea,                         -- E_v
  ADD COLUMN kmac            bytea,                         -- HKDF(S) reported by invitee device
  ADD COLUMN kmac_commit     bytea,                         -- sha256(HKDF(S)) reported by inviter device
  ADD COLUMN invitee_sig_pub bytea,                         -- separate non-extractable WebCrypto ECDSA P-256 key of the invitee
                                                            -- device; every later join call is signed over (hs, server challenge)
  ADD COLUMN play_at         timestamptz,                   -- SERVER time the play-start was issued
  ADD COLUMN report_at       timestamptz,                   -- SERVER time the inviter's decode report arrived
  ADD COLUMN report          jsonb,                         -- inviter decode report (bits + client timings, which can only TIGHTEN)
  ADD COLUMN attempts        smallint NOT NULL DEFAULT 0 CHECK (attempts <= 5),
  ADD COLUMN outcome         smallint NOT NULL DEFAULT 0;   -- 0 none, 1 retry, 2 accepted, 3 verified
