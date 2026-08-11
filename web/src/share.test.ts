import { expect, test } from 'bun:test';
import {
  buildShareSnapshot,
  buildShareURL,
  decryptShareSnapshot,
  encryptShareSnapshot,
  generateShareOwnerRef,
  isShareOwnerRef,
  parseSharePath,
} from './share';

const conversation = {
  id: 'local-id-that-must-not-leak',
  title: 'Useful thread',
  createdAt: Date.parse('2026-08-01T00:00:00Z'),
  updatedAt: Date.parse('2026-08-02T00:00:00Z'),
  modelId: 'private-runtime-id',
  shareOwnerRef: 'private-replacement-token',
  cards: [{
    id: 'card-id', category: 'AI' as const, title: 'Signal', bullets: ['one'], sources: ['@source'],
    timestamp: new Date('2026-08-01T01:00:00Z'), rawMarkdown: 'duplicated private context',
  }],
  chatItems: [
    { kind: 'chat' as const, id: 'u1', role: 'user' as const, text: 'question', loading: false },
    { kind: 'chat' as const, id: 'a1', role: 'assistant' as const, text: 'answer', loading: false, modelLabel: 'Sonnet' },
    { kind: 'chat' as const, id: 'a2', role: 'assistant' as const, text: 'unfinished secret', loading: true },
  ],
};

test('buildShareSnapshot allowlists completed public fields', () => {
  const snapshot = buildShareSnapshot(conversation);
  const encoded = JSON.stringify(snapshot);
  expect(snapshot.messages.length).toBe(2);
  expect(encoded.includes('local-id-that-must-not-leak')).toBe(false);
  expect(encoded.includes('private-runtime-id')).toBe(false);
  expect(encoded.includes('private-replacement-token')).toBe(false);
  expect(encoded.includes('duplicated private context')).toBe(false);
  expect(encoded.includes('unfinished secret')).toBe(false);
  expect(encoded.includes('loading')).toBe(false);
});

test('share replacement references are opaque 256-bit values', () => {
  const first = generateShareOwnerRef();
  const second = generateShareOwnerRef();
  expect(isShareOwnerRef(first)).toBe(true);
  expect(first.length).toBe(43);
  expect(first === second).toBe(false);
  expect(isShareOwnerRef('conv-1234-local-id')).toBe(false);
});

test('encrypted snapshot round trips without plaintext in the envelope', async () => {
  const snapshot = buildShareSnapshot(conversation);
  const { envelope, key } = await encryptShareSnapshot(snapshot);
  expect(JSON.stringify(envelope).includes('question')).toBe(false);
  expect(JSON.stringify(envelope).includes(key)).toBe(false);
  expect(key.length).toBe(43);
  expect(await decryptShareSnapshot(envelope, key)).toEqual(snapshot);
});

test('wrong fragment key cannot decrypt a snapshot', async () => {
  const snapshot = buildShareSnapshot(conversation);
  const { envelope } = await encryptShareSnapshot(snapshot);
  const other = await encryptShareSnapshot(snapshot);
  let rejected = false;
  try {
    await decryptShareSnapshot(envelope, other.key);
  } catch {
    rejected = true;
  }
  expect(rejected).toBe(true);
});

test('share paths and URLs are strict and keep the key in the fragment', () => {
  const id = 'A'.repeat(43);
  const key = 'B'.repeat(43);
  expect(parseSharePath(`/share/${id}`)).toBe(id);
  expect(parseSharePath(`/share/${id}/`)).toBe(id);
  expect(parseSharePath(`/share/${id}/extra`)).toBe(null);
  expect(parseSharePath('/share/../../api/chat')).toBe(null);
  const url = buildShareURL('https://birdy.example', `/share/${id}`, key);
  expect(url).toBe(`https://birdy.example/share/${id}#${key}`);
});
