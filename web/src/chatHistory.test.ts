import { expect, test } from 'bun:test';
import {
  buildChatMessages,
  chatWindowStep,
  contextToAttach,
  maxChatBytes,
  maxChatMessages,
  maxContextBytes,
  scanContext,
  turnContent,
} from './chatHistory';
import type { ChatTurn, HistoryItem } from './chatHistory';

const user = (text: string, extra: Partial<HistoryItem> = {}): HistoryItem => ({ role: 'user', text, loading: false, ...extra });
const assistant = (text: string): HistoryItem => ({ role: 'assistant', text, loading: false });

// Simulates the client: send, then store the user turn and the finished answer.
function chat(turns: number, opts: { context?: (i: number) => string; answer?: (i: number) => string } = {}) {
  let items: HistoryItem[] = [];
  const sent: ChatTurn[][] = [];
  for (let i = 1; i <= turns; i++) {
    const current = opts.context?.(i) ?? '';
    const context = contextToAttach(items, current);
    const next = user(`question ${i}`, context ? { context } : {});
    sent.push(buildChatMessages(items, next));
    items = [...items, next, assistant(opts.answer?.(i) ?? `answer ${i}`)];
  }
  return sent;
}

const startsWith = (longer: ChatTurn[], shorter: ChatTurn[]) =>
  shorter.every((turn, i) => JSON.stringify(longer[i]) === JSON.stringify(turn));

test('turn N+1 begins with turn N exactly', () => {
  const sent = chat(6);
  for (let i = 1; i < sent.length; i++) {
    expect(startsWith(sent[i], sent[i - 1])).toBe(true);
    expect(sent[i].length).toBe(sent[i - 1].length + 2);
    expect(sent[i].at(-1)).toEqual({ role: 'user', content: `question ${i + 1}` });
  }
});

test('the scan context rides on the turn that introduced it and is not repeated', () => {
  const scanA = scanContext([{ rawMarkdown: 'headline A' }]);
  const scanB = scanContext([{ rawMarkdown: 'headline B' }]);
  const sent = chat(4, { context: i => (i < 3 ? scanA : scanB) });
  expect(sent[0][0].content).toBe(`${scanA}question 1`);
  expect(sent[1][2].content).toBe('question 2');
  expect(sent[2][4].content).toBe(`${scanB}question 3`);
  expect(sent[3][6].content).toBe('question 4');
  for (let i = 1; i < sent.length; i++) expect(startsWith(sent[i], sent[i - 1])).toBe(true);
});

test('a Deep Dive replays the instructions it sent, not its label', () => {
  const item = user('Deep dive: topic', { sent: 'Explain this topic in depth.' });
  expect(turnContent(item)).toBe('Explain this topic in depth.');
  const sent = buildChatMessages([item, assistant('deep answer')], user('and then?'));
  expect(sent[0].content).toBe('Explain this topic in depth.');
});

test('unfinished answers are skipped the same way every time', () => {
  const items = [user('q1'), { role: 'assistant', text: '', loading: true } as HistoryItem, user('q2'), assistant('a2')];
  const first = buildChatMessages(items, user('q3'));
  expect(first.map(t => t.content)).toEqual(['q1', 'q2', 'a2', 'q3']);
  const second = buildChatMessages([...items, user('q3'), assistant('a3')], user('q4'));
  expect(startsWith(second, first)).toBe(true);
});

test('a long conversation moves its window in steps, not one turn per request', () => {
  const sent = chat(40, { context: () => scanContext([{ rawMarkdown: 'headline' }]) });
  let shifts = 0;
  for (let i = 1; i < sent.length; i++) {
    expect(sent[i].length <= maxChatMessages).toBe(true);
    expect(sent[i][0].role).toBe('user');
    if (!startsWith(sent[i], sent[i - 1])) shifts++;
  }
  // 80 stored items through a 40-message window moved in 10-message steps.
  expect(shifts <= Math.ceil((80 - maxChatMessages) / chatWindowStep)).toBe(true);
  expect(shifts > 0).toBe(true);
  // The scan introduced on turn 1 stays in view after it scrolls out.
  expect(sent.at(-1)![0].content.startsWith('Public X Explore scan:\nheadline')).toBe(true);
});

test('history stays under the byte budget by dropping whole steps', () => {
  const big = 'x'.repeat(20 * 1024);
  const sent = chat(12, { answer: () => big });
  const last = sent.at(-1)!;
  const size = last.reduce((total, turn) => total + new TextEncoder().encode(turn.content).byteLength, 0);
  expect(size <= maxChatBytes).toBe(true);
  expect(last[0].role).toBe('user');
  expect(last.at(-1)!.content).toBe('question 12');
});

test('an oversized scan keeps only the cards that fit', () => {
  const card = { rawMarkdown: 'y'.repeat(20 * 1024) };
  const context = scanContext([card, card, card]);
  expect(new TextEncoder().encode(context).byteLength <= maxContextBytes).toBe(true);
  expect(context.split('y'.repeat(20 * 1024)).length - 1).toBe(2);
});

test('one oversized stored turn scrolls out instead of blocking the conversation', () => {
  const huge = 'z'.repeat(maxChatBytes + 1);
  const items = [user('q1'), assistant(huge), user('q2'), assistant('a2')];
  const sent = buildChatMessages(items, user('q3'));
  // The window moves a whole step past it; the conversation keeps working.
  expect(sent.at(-1)!.content).toBe('q3');
  expect(sent.some(t => t.content === huge)).toBe(false);
  expect(sent[0].role).toBe('user');
});
