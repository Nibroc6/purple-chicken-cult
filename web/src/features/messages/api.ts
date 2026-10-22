// Direct-messages API calls (messagesapi) and their view models. Owner: direct
// messages (contract request dm-1).
import { api, unwrap } from '../../api/client';
import { ApiError, errorKey } from '../../api/errors';
import type { components } from '../../api/schema.gen';
import { parse } from '../../markdown';
import type { MarkdownDoc, PostVM, Ref } from '../../vm/types';
import { asRef } from '../../vm/types';
import { toPostVM } from '../post/vm';

type S = components['schemas'];

export const messagesKey = ['messages'] as const;
export const listKey = [...messagesKey, 'list'] as const;
export const convKey = (ref: Ref) => [...messagesKey, 'conv', ref] as const;
export const threadKey = (ref: Ref) => [...messagesKey, 'thread', ref] as const;

// MaxBody bounds a message in code points (server: 2,000); MaxTitle a group title.
export const MaxBody = 2000;
export const MaxTitle = 80;
// MaxMembers bounds a group, the creator and invitations included.
export const MaxMembers = 32;

export interface MemberVM {
  ref: Ref;
  handle: string;
  displayName: string;
  admin: boolean;
  // The viewer's own edge with them ('' when not connected).
  relation: '' | 'friend' | 'friend_plus';
}

export interface PreviewVM {
  kind: 'text' | 'post' | 'event' | 'deleted';
  excerpt: string;
  event?: EventKind;
  fromMe: boolean;
  from?: string;
  at: Date;
}

export type EventKind = 'created' | 'joined' | 'left' | 'removed' | 'renamed';

export interface ConversationVM {
  ref: Ref;
  kind: 'direct' | 'group';
  state: 'member' | 'invited';
  title: string;
  members: MemberVM[];
  memberCount: number;
  invitedBy?: MemberVM;
  admin: boolean;
  canSend: boolean;
  muted: boolean;
  unread: number;
  last?: PreviewVM;
  updatedAt: Date;
}

export interface MessageVM {
  ref: Ref;
  kind: 'text' | 'event' | 'deleted';
  mine: boolean;
  from?: MemberVM;
  body: MarkdownDoc | null;
  // A shared post: the post when the viewer can see it, 'unavailable' when not.
  post?: PostVM | 'unavailable';
  event?: EventKind;
  subject?: MemberVM;
  at: Date;
}

export interface Page<T> {
  items: T[];
  next?: string;
}

function toMember(m: S['ConvMember']): MemberVM {
  return { ref: asRef(m.ref), handle: m.handle, displayName: m.display_name, admin: m.admin === true, relation: m.relation ?? '' };
}

export function toConversationVM(c: S['ConversationView']): ConversationVM {
  return {
    ref: asRef(c.ref),
    kind: c.kind,
    state: c.state,
    title: c.title ?? '',
    members: c.members.map(toMember),
    memberCount: c.member_count,
    ...(c.invited_by ? { invitedBy: toMember(c.invited_by) } : {}),
    admin: c.admin === true,
    canSend: c.can_send,
    muted: c.muted === true,
    unread: c.unread,
    ...(c.last
      ? {
          last: {
            kind: c.last.kind,
            excerpt: c.last.excerpt ?? '',
            ...(c.last.event ? { event: c.last.event } : {}),
            fromMe: c.last.from_me,
            ...(c.last.from ? { from: c.last.from } : {}),
            at: new Date(c.last.at),
          },
        }
      : {}),
    updatedAt: new Date(c.updated_at),
  };
}

export function toMessageVM(m: S['MessageView']): MessageVM {
  let post: MessageVM['post'];
  if (m.post) post = m.post.post ? toPostVM(m.post.post) : 'unavailable';
  return {
    ref: asRef(m.ref),
    kind: m.kind,
    mine: m.mine,
    ...(m.from ? { from: toMember(m.from) } : {}),
    body: m.body ? parse(m.body, []) : null,
    ...(post ? { post } : {}),
    ...(m.event ? { event: m.event } : {}),
    ...(m.subject ? { subject: toMember(m.subject) } : {}),
    at: new Date(m.at),
  };
}

const at = (ref: Ref) => ({ params: { path: { ref: ref as string } } });

function ok(res: { response: Response; error?: unknown }): void {
  if (!res.response.ok) throw new ApiError(res.response.status, res.error);
}

export async function fetchConversations(cursor?: string): Promise<Page<ConversationVM>> {
  const page = unwrap(await api.GET('/messages', { params: { query: cursor ? { cursor } : {} } }));
  return { items: page.items.map(toConversationVM), ...(page.next ? { next: page.next } : {}) };
}

export async function fetchUnread(): Promise<number> {
  return unwrap(await api.GET('/messages/unread')).unread;
}

export async function fetchConversation(ref: Ref): Promise<ConversationVM> {
  return toConversationVM(unwrap(await api.GET('/messages/{ref}', at(ref))));
}

export async function fetchMessages(ref: Ref, cursor?: string): Promise<Page<MessageVM>> {
  const page = unwrap(
    await api.GET('/messages/{ref}/messages', { params: { path: { ref: ref }, query: cursor ? { cursor } : {} } }),
  );
  return { items: page.items.map(toMessageVM), ...(page.next ? { next: page.next } : {}) };
}

export async function openDirect(to: Ref): Promise<ConversationVM> {
  return toConversationVM(unwrap(await api.POST('/messages/direct', { body: { to: to } })));
}

export async function createGroup(title: string, members: Ref[]): Promise<ConversationVM> {
  return toConversationVM(unwrap(await api.POST('/messages/groups', { body: { title, members: members } })));
}

export async function sendMessage(ref: Ref, body: string, post?: Ref): Promise<MessageVM> {
  const res = await api.POST('/messages/{ref}/messages', { ...at(ref), body: { body, ...(post ? { post: post } : {}) } });
  return toMessageVM(unwrap(res));
}

export async function deleteMessage(ref: Ref, msg: Ref): Promise<void> {
  ok(await api.DELETE('/messages/{ref}/messages/{id}', { params: { path: { ref: ref, id: msg } } }));
}

export async function markRead(ref: Ref, upTo: Ref): Promise<void> {
  ok(await api.POST('/messages/{ref}/read', { ...at(ref), body: { up_to: upTo } }));
}

export async function setMuted(ref: Ref, on: boolean): Promise<void> {
  ok(on ? await api.PUT('/messages/{ref}/mute', at(ref)) : await api.DELETE('/messages/{ref}/mute', at(ref)));
}

export async function leave(ref: Ref): Promise<void> {
  ok(await api.DELETE('/messages/{ref}', at(ref)));
}

export async function join(ref: Ref): Promise<ConversationVM> {
  return toConversationVM(unwrap(await api.POST('/messages/{ref}/join', at(ref))));
}

export async function invite(ref: Ref, people: Ref[]): Promise<ConversationVM> {
  return toConversationVM(unwrap(await api.POST('/messages/{ref}/members', { ...at(ref), body: { people: people } })));
}

export async function removeMember(ref: Ref, person: Ref): Promise<void> {
  ok(await api.DELETE('/messages/{ref}/members/{id}', { params: { path: { ref: ref, id: person } } }));
}

export async function rename(ref: Ref, title: string): Promise<ConversationVM> {
  return toConversationVM(unwrap(await api.PATCH('/messages/{ref}', { ...at(ref), body: { title } })));
}

// The problem codes this feature words itself (messages:errors.<code>).
const ownErrors = new Set([
  'not_connected',
  'recipient_self',
  'not_joined',
  'members_empty',
  'too_many_members',
  'title_too_long',
  'probation',
  'not_group',
  'not_admin',
  'remove_self',
  'body_empty',
  'body_too_long',
]);

// i18n-dynamic: messages:errors.
export function messageErrorKey(err: unknown): string {
  if (err instanceof ApiError && ownErrors.has(err.code)) return `messages:errors.${err.code}`;
  return errorKey(err);
}

// conversationName is how a conversation is called: the group's title, else
// its members' handles.
export function conversationName(c: ConversationVM, t: (k: 'name.untitled', o: { count: number }) => string): string {
  if (c.title) return c.title;
  if (c.members.length > 0) return c.members.map((m) => `@${m.handle}`).join(', ');
  return t('name.untitled', { count: c.memberCount });
}
