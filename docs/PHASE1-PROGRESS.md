# Phase 1 progress

The plan is [IMPLEMENTATION.md](IMPLEMENTATION.md) ("Phase 1"); package rules are in
[SCAFFOLD.md](SCAFFOLD.md). This file says what has landed and what each landed package
left for the others, so the next package can start without re-reading the history.

| Package | State |
|---|---|
| SCAFFOLD-1 | landed (5dc55d2) |
| P1-W01 custodial keys and co-signing | landed, see below |
| P1-W02 … P1-W13, INT-1 | not started |

## P1-W01: what landed

- **Custodial keys** (`gate/records/userkeys.go`): one Ed25519 key per user, created in the
  transaction of the user's first authored record (signup writes one), wrapped with
  AES-256-GCM under `PCC_KEK` with AAD `key_id ‖ owner_id`. Dev and test without
  `PCC_KEK` derive a KEK from the master key; prod refuses to start without one (config).
  Account deletion destroys the wrapped key and closes its window; the public key stays.
- **Signer v2:** every record with an author is signed by the author's key and
  countersigned by the server key (`cosigs`); server-authored records keep the server key.
  Stored assertions carry the credential's COSE key and owner, taken from its
  PasskeyChange record. `Sign` accepts a co-sign draft as `json.RawMessage` and stores
  it byte for byte.
- **Intents** (`gate/cosign`, `POST /api/cosign`): drafters registered per purpose,
  challenge over the draft, single use, five minutes, consumed in the caller's
  transaction, body checked against the draft, assertion verified by
  `Passkeys.VerifyAssertion` and pinned to the intent's challenge.
- **Export:** `keys.json` lists the user keys that signed exported records, the origin and
  `cosign_required_from`. `VerifyExport` checks countersignatures, every assertion
  offline, the credential chain, and (from the cutover) missing assertions. New failure
  codes: `bad_cosig`, `bad_assertion`, `missing_assertion`, `unchained_passkey`.
- **Web** (`features/cosign`): `requestCosign({purpose, target})` opens the global dialog,
  which shows the record and returns `{intent, assertion}` for the write's body.
- Contract additions: [p1-w01-1](contract-requests/p1-w01-1.md).

## Left for other packages

- **Callers register drafters and call `Require`:** P1-W03 (friend_request, friend_accept,
  unfriend, block), P1-W02 (invite_inviter), P1-W07 (passkey_add, passkey_remove), P1-W10
  (account_delete, recovery_approve), P1-W06 (mod_action). Until they do, `POST /api/cosign`
  answers 404 for those purposes, and nothing in the app asks for a co-signature yet.
- **Signup ceremony for invitees (fidelity #21)** needs P1-W02 (join flow) and P1-W07
  (an assertion with the just-created credential in the guest session, before
  RegisterFinish commits). `sigs.VerifyAssertion` can check that assertion against the new
  credential's COSE key directly.
- **The cutover:** `records.Service.SetCosignRequiredFrom` makes assertions mandatory in
  exports from a given time. INT-1 sets it once every high-value write co-signs.
- **e2e:** `web/e2e/p1-w01.spec.ts` is not written yet, since no purpose has a production
  drafter; the first caller package should drive the dialog through the virtual
  authenticator.
