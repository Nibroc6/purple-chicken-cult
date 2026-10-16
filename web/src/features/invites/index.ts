// Public surface of the invites feature for other features: the initiator's
// side of an in-person handshake, for the friend+ upgrade (people, P1-W03:
// POST /api/friends/{ref}/upgrade answers a HandshakeView) and the recovery
// handshake (recovery, P1-W10). Owner: P1-W02.
export { HandshakeHost } from './HandshakeHost';
export { toOpenedVM, type OpenedVM } from './api';
