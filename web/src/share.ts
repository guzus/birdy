export const shareRoutePrefix = '/share/';
export const shareKeyBytes = 32;
export const shareIVBytes = 12;
export const maxSharePlaintextBytes = 350 * 1024;

export type SharedCard = {
  category: 'CRYPTO' | 'AI' | 'TRENDING' | 'SIGNAL' | 'RESEARCH';
  title: string;
  bullets: string[];
  sources: string[];
  timestamp: string;
};

export type SharedMessage = {
  role: 'user' | 'assistant';
  text: string;
  modelLabel?: string;
};

export type ShareSnapshot = {
  version: 1;
  title: string;
  conversationCreatedAt: string;
  cards: SharedCard[];
  messages: SharedMessage[];
};

export type ShareEnvelope = {
  version: 1;
  algorithm: 'AES-GCM';
  iv: string;
  ciphertext: string;
};

type ConversationLike = {
  title: string;
  createdAt: number;
  cards: Array<{
    category: SharedCard['category'];
    title: string;
    bullets: string[];
    sources: string[];
    timestamp: Date | string;
  }>;
  chatItems: Array<{
    role: SharedMessage['role'];
    text: string;
    loading: boolean;
    modelLabel?: string;
  }>;
};

const encoder = new TextEncoder();
const decoder = new TextDecoder();

export function base64urlEncode(bytes: Uint8Array<ArrayBufferLike>): string {
  let binary = '';
  for (const byte of bytes) binary += String.fromCharCode(byte);
  return btoa(binary).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/g, '');
}

export function base64urlDecode(value: string): Uint8Array<ArrayBuffer> {
  if (!/^[A-Za-z0-9_-]+$/.test(value)) throw new Error('invalid share key');
  const padded = value.replace(/-/g, '+').replace(/_/g, '/') + '='.repeat((4 - value.length % 4) % 4);
  const binary = atob(padded);
  return new Uint8Array(Uint8Array.from(binary, character => character.charCodeAt(0)));
}

export function parseSharePath(pathname: string): string | null {
  const match = pathname.match(/^\/share\/([A-Za-z0-9_-]{43})\/?$/);
  return match?.[1] ?? null;
}

export function generateShareOwnerRef(): string {
  return base64urlEncode(crypto.getRandomValues(new Uint8Array(shareKeyBytes)));
}

export function isShareOwnerRef(value: unknown): value is string {
  return typeof value === 'string' && /^[A-Za-z0-9_-]{43}$/.test(value);
}

export function buildShareSnapshot(conversation: ConversationLike): ShareSnapshot {
  const cards = conversation.cards.slice(0, 100).map(card => ({
    category: card.category,
    title: card.title.slice(0, 500),
    bullets: card.bullets.slice(0, 100).map(bullet => bullet.slice(0, 10_000)),
    sources: card.sources.slice(0, 100).map(source => source.slice(0, 2_000)),
    timestamp: new Date(card.timestamp).toISOString(),
  }));
  const messages = conversation.chatItems
    .filter(item => !item.loading && item.text.trim())
    .slice(0, 500)
    .map(item => ({
      role: item.role,
      text: item.text.slice(0, 100_000),
      ...(item.modelLabel?.trim() ? { modelLabel: item.modelLabel.slice(0, 100) } : {}),
    }));

  if (cards.length === 0 && messages.length === 0) {
    throw new Error('There is nothing completed to share yet.');
  }
  const snapshot: ShareSnapshot = {
    version: 1,
    title: (conversation.title.trim() || 'Birdy conversation').slice(0, 500),
    conversationCreatedAt: new Date(conversation.createdAt).toISOString(),
    cards,
    messages,
  };
  if (encoder.encode(JSON.stringify(snapshot)).byteLength > maxSharePlaintextBytes) {
    throw new Error('This conversation is too large to share.');
  }
  return snapshot;
}

export async function encryptShareSnapshot(snapshot: ShareSnapshot): Promise<{ envelope: ShareEnvelope; key: string }> {
  const rawKey = crypto.getRandomValues(new Uint8Array(shareKeyBytes));
  const iv = crypto.getRandomValues(new Uint8Array(shareIVBytes));
  const key = await crypto.subtle.importKey('raw', rawKey, 'AES-GCM', false, ['encrypt']);
  const ciphertext = await crypto.subtle.encrypt({ name: 'AES-GCM', iv }, key, encoder.encode(JSON.stringify(snapshot)));
  return {
    envelope: {
      version: 1,
      algorithm: 'AES-GCM',
      iv: base64urlEncode(iv),
      ciphertext: base64urlEncode(new Uint8Array(ciphertext)),
    },
    key: base64urlEncode(rawKey),
  };
}

function isShareSnapshot(value: unknown): value is ShareSnapshot {
  if (!value || typeof value !== 'object') return false;
  const snapshot = value as Partial<ShareSnapshot>;
  if (snapshot.version !== 1 || typeof snapshot.title !== 'string' || typeof snapshot.conversationCreatedAt !== 'string') return false;
  if (!Array.isArray(snapshot.cards) || !Array.isArray(snapshot.messages)) return false;
  return snapshot.cards.every(card => {
    if (!card || typeof card !== 'object') return false;
    const typed = card as Partial<SharedCard>;
    return ['CRYPTO', 'AI', 'TRENDING', 'SIGNAL', 'RESEARCH'].includes(typed.category ?? '')
      && typeof typed.title === 'string'
      && typeof typed.timestamp === 'string'
      && Array.isArray(typed.bullets) && typed.bullets.every(item => typeof item === 'string')
      && Array.isArray(typed.sources) && typed.sources.every(item => typeof item === 'string');
  }) && snapshot.messages.every(message => {
    if (!message || typeof message !== 'object') return false;
    const typed = message as Partial<SharedMessage>;
    return (typed.role === 'user' || typed.role === 'assistant')
      && typeof typed.text === 'string'
      && (typed.modelLabel === undefined || typeof typed.modelLabel === 'string');
  });
}

export async function decryptShareSnapshot(envelope: ShareEnvelope, encodedKey: string): Promise<ShareSnapshot> {
  if (envelope.version !== 1 || envelope.algorithm !== 'AES-GCM') throw new Error('unsupported share');
  if (encodedKey.length !== 43) throw new Error('invalid share key');
  const rawKey = base64urlDecode(encodedKey);
  const iv = base64urlDecode(envelope.iv);
  const ciphertext = base64urlDecode(envelope.ciphertext);
  if (rawKey.byteLength !== shareKeyBytes || iv.byteLength !== shareIVBytes) throw new Error('invalid share key');
  const key = await crypto.subtle.importKey('raw', rawKey, 'AES-GCM', false, ['decrypt']);
  const plaintext = await crypto.subtle.decrypt({ name: 'AES-GCM', iv }, key, ciphertext);
  const snapshot: unknown = JSON.parse(decoder.decode(plaintext));
  if (!isShareSnapshot(snapshot)) throw new Error('invalid share snapshot');
  return snapshot;
}

export function buildShareURL(origin: string, path: string, encodedKey: string): string {
  const url = new URL(path, origin);
  url.hash = encodedKey;
  return url.toString();
}
