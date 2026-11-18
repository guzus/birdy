export const scanSourceLabels = {
  home: 'Home timeline',
  crypto: 'Crypto search',
  ai: 'AI search',
  news: 'News',
} as const;

export type ScanSource = keyof typeof scanSourceLabels;
export type ScanPost = { id: string; url: string; text: string; author?: string };
export type ScanCategory = 'CRYPTO' | 'AI' | 'TRENDING' | 'SIGNAL' | 'RESEARCH';
export type ScanCard = {
  category: ScanCategory;
  title: string;
  summary: string;
  bullets: string[];
  sources: ScanPost[];
};
export type ScanEvent =
  | { type: 'source'; source: ScanSource; status: 'ok' | 'error'; posts: ScanPost[] }
  | ({ type: 'card' } & ScanCard)
  | { type: 'done' };

export function scanPermalink(value: string): boolean {
  if (/^https:\/\/x\.com\/(?:[a-zA-Z0-9_]{1,15}\/status|i\/web\/status|i\/(?:trending|events|news))\/[0-9]+$/.test(value)) return true;
  if (!value.startsWith('https://x.com/search?q=')) return false;
  const url = new URL(value);
  return !url.hash && [...url.searchParams.keys()].join() === 'q' && Boolean(url.searchParams.get('q')?.trim());
}

function validPost(post: unknown): post is ScanPost {
  if (!post || typeof post !== 'object') return false;
  const p = post as Partial<ScanPost>;
  return typeof p.id === 'string' && typeof p.url === 'string' && scanPermalink(p.url)
    && typeof p.text === 'string' && (p.author === undefined || typeof p.author === 'string');
}

export async function readScanStream(body: ReadableStream<Uint8Array>, deliver: (event: ScanEvent) => void) {
  const reader = body.getReader();
  const decoder = new TextDecoder();
  const evidence = new Map<string, ScanPost>();
  const sources = new Set<ScanSource>();
  let buffer = '';
  try {
    while (true) {
      const { value, done } = await reader.read();
      if (done) throw new Error('The scan ended before it finished.');
      buffer += decoder.decode(value, { stream: true });
      let end: number;
      while ((end = buffer.indexOf('\n\n')) >= 0) {
        const frame = buffer.slice(0, end);
        buffer = buffer.slice(end + 2);
        const data = frame.split('\n').filter(line => line.startsWith('data:')).map(line => line.slice(5).trimStart()).join('\n');
        if (!data) continue;
        const event = JSON.parse(data);
        if (event.type === 'error') throw new Error(typeof event.error === 'string' ? event.error : 'Scan failed.');
        if (event.type === 'done') {
          if (sources.size !== Object.keys(scanSourceLabels).length) throw new Error('The scan ended before all sources returned.');
          deliver({ type: 'done' });
          return;
        }
        if (event.type === 'source') {
          if (!Object.hasOwn(scanSourceLabels, event.source) || sources.has(event.source)
            || !['ok', 'error'].includes(event.status) || !Array.isArray(event.posts) || !event.posts.every(validPost)) throw new Error('Invalid scan source.');
          sources.add(event.source);
          if (event.status === 'ok') for (const post of event.posts) evidence.set(post.url, post);
          deliver(event);
        } else if (event.type === 'card') {
          if (!['CRYPTO', 'AI', 'TRENDING', 'SIGNAL', 'RESEARCH'].includes(event.category)
            || typeof event.title !== 'string' || typeof event.summary !== 'string' || !Array.isArray(event.bullets)
            || !event.bullets.every((b: unknown) => typeof b === 'string') || !Array.isArray(event.sources)) throw new Error('Invalid scan summary.');
          const cited: ScanPost[] = [];
          for (const candidate of event.sources) {
            const post = evidence.get(candidate?.url);
            if (!post || post.id !== candidate?.id) throw new Error('The summary cited a source outside this scan.');
            if (!cited.includes(post)) cited.push(post);
          }
          if (!cited.length) throw new Error('The summary did not cite a source.');
          deliver({ ...event, sources: cited });
        } else {
          throw new Error('Unexpected scan event.');
        }
      }
    }
  } finally {
    try { await reader.cancel(); } finally { reader.releaseLock(); }
  }
}
