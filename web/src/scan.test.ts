import { test, expect } from 'bun:test';
import { readScanStream, scanPermalink } from './scan';
import type { ScanEvent } from './scan';

const home = { id: '123', text: 'An X post', url: 'https://x.com/i/web/status/123' };
const source = (name: string, posts: typeof home[] = [], status = 'ok') => ({ type: 'source', source: name, status, posts });
const card = { type: 'card', category: 'AI', title: 'News', summary: 'A summary', bullets: [], sources: [home] };
const frame = (event: unknown) => new TextEncoder().encode('data: ' + JSON.stringify(event) + '\n\n');
function stream(events: unknown[]) {
  return new ReadableStream<Uint8Array>({
    start(c) {
      for (const event of events) c.enqueue(frame(event));
      c.close();
    },
  });
}

test('delivers the home post and sourced cards before termination across chunk boundaries', async () => {
  let writer!: ReadableStreamDefaultController<Uint8Array>;
  const body = new ReadableStream<Uint8Array>({ start(c) { writer = c; } });
  const received: ScanEvent[] = [];
  let sawHome!: () => void, sawCard!: () => void;
  const homeArrived = new Promise<void>(r => { sawHome = r; });
  const cardArrived = new Promise<void>(r => { sawCard = r; });
  const done = readScanStream(body, event => {
    received.push(event);
    if (event.type === 'source' && event.source === 'home') sawHome();
    if (event.type === 'card') sawCard();
  });
  const bytes = frame(source('home', [home]));
  writer.enqueue(bytes.slice(0, 9)); writer.enqueue(bytes.slice(9));
  await homeArrived;
  expect(received.length).toBe(1);
  for (const item of [source('ai'), source('crypto', [], 'error'), source('news'), card]) writer.enqueue(frame(item));
  await cardArrived;
  expect(received[4]).toEqual(card);
  writer.enqueue(frame({ type: 'done' }));
  await done;
  expect(received.map(e => e.type)).toEqual(['source', 'source', 'source', 'source', 'card', 'done']);
});

test('rejects unverified citations, provider failures and interrupted scans', async () => {
  const cases: [unknown[], string][] = [
    [[source('home', [home]), { ...card, sources: [{ ...home, url: 'https://x.com/i/web/status/789' }] }], 'outside'],
    [[{ type: 'error', error: 'failed' }], 'failed'],
    [[source('home')], 'ended'],
    [[source('home'), { type: 'done' }], 'all sources'],
  ];
  for (const [events, message] of cases) {
    const result = await readScanStream(stream(events), () => {}).then(() => '', err => String(err));
    expect(result).toContain(message);
  }
});
test('renders only supported canonical post and news permalinks', () => {
  for (const value of [home.url, 'https://x.com/i/events/123', 'https://x.com/search?q=some%20news']) expect(scanPermalink(value)).toBe(true);
  for (const value of ['https://x.com.bad/i/web/status/123', 'javascript:alert(1)', 'https://x.com/search?q=hi&to=else', 'https://x.com/i/web/status/123?x=3']) expect(scanPermalink(value)).toBe(false);
});
