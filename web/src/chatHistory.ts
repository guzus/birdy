// Builds the conversation the server receives as real turns.
//
// Provider prompt caches match an exact prefix, so a conversation's next
// request must begin with its previous one byte for byte. Every turn is
// therefore replayed exactly as it was first sent: changing context (a scan)
// is stored on the turn that introduced it, and the history window only moves
// in large steps instead of dropping one old turn per request.

export type ChatRole = 'user' | 'assistant';
export type ChatTurn = { role: ChatRole; content: string };

export type HistoryItem = {
  role: ChatRole;
  text: string;
  loading: boolean;
  // Context this turn introduced, sent ahead of its text.
  context?: string;
  // What was sent instead of the displayed text (a Deep Dive's instructions).
  sent?: string;
};

// The server accepts 48 messages, 64 KiB each and 192 KiB in all.
export const maxChatMessages = 40;
export const chatWindowStep = 10;
export const maxChatBytes = 160 * 1024;
export const maxContextBytes = 48 * 1024;

const encoder = new TextEncoder();
const byteLength = (value: string) => encoder.encode(value).byteLength;

export function turnContent(item: Pick<HistoryItem, 'text' | 'context' | 'sent'>): string {
  return (item.context ?? '') + (item.sent ?? item.text);
}

export function scanContext(cards: { rawMarkdown: string }[]): string {
  for (let count = cards.length; count > 0; count--) {
    const context = 'Public X Explore scan:\n' + cards.slice(0, count).map(card => card.rawMarkdown).join('\n\n') + '\n\n';
    if (byteLength(context) <= maxContextBytes) return context;
  }
  return '';
}

// The context a new turn must carry: the current one, unless the latest turn
// that introduced context already introduced exactly this.
export function contextToAttach(items: HistoryItem[], current: string): string | undefined {
  if (!current) return undefined;
  for (let i = items.length - 1; i >= 0; i--) {
    const context = items[i].context;
    if (context) return context === current ? undefined : current;
  }
  return current;
}

function windowTurns(all: HistoryItem[], start: number): ChatTurn[] {
  const kept = all.slice(start);
  const turns = kept.map(item => ({ role: item.role, content: turnContent(item) }));
  // A window that dropped the turn introducing the newest context carries it
  // on its first turn, the same way for every request at this window.
  if (start > 0 && !kept.some(item => item.context)) {
    const dropped = all.slice(0, start).reverse().find(item => item.context)?.context;
    if (dropped) turns[0] = { ...turns[0], content: dropped + turns[0].content };
  }
  return turns;
}

export function buildChatMessages(items: HistoryItem[], next: HistoryItem): ChatTurn[] {
  const all = [...items.filter(item => !item.loading && item.text.trim()), next];
  const last = all.length - 1;
  let start = all.length > maxChatMessages
    ? Math.ceil((all.length - maxChatMessages) / chatWindowStep) * chatWindowStep
    : 0;
  for (;;) {
    start = Math.min(start, last);
    while (start < last && all[start].role !== 'user') start++;
    const turns = windowTurns(all, start);
    const size = turns.reduce((total, turn) => total + byteLength(turn.content), 0);
    if (size <= maxChatBytes || start === last) return turns;
    start += chatWindowStep;
  }
}
