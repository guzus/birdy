import { useCallback, useEffect, useRef, useState } from 'react';

const inviteCodeKey = 'birdy_host_invite_code';

type CardCategory = 'CRYPTO' | 'AI' | 'TRENDING' | 'SIGNAL' | 'RESEARCH';

type AlphaCard = {
  id: string;
  category: CardCategory;
  title: string;
  bullets: string[];
  sources: string[];
  timestamp: Date;
  rawMarkdown: string;
};

type FeedItem =
  | { kind: 'card'; card: AlphaCard }
  | { kind: 'chat'; id: string; role: 'user' | 'assistant'; text: string; loading: boolean };

const categoryMeta: Record<CardCategory, { icon: string; label: string }> = {
  CRYPTO: { icon: '\u{1F525}', label: 'CRYPTO' },
  AI: { icon: '\u{1F916}', label: 'AI' },
  TRENDING: { icon: '\u{1F4C8}', label: 'TRENDING' },
  SIGNAL: { icon: '\u{1F4E1}', label: 'SIGNAL' },
  RESEARCH: { icon: '\u{1F50D}', label: 'RESEARCH' },
};

const SCAN_PROMPT = `You are birdy's alpha radar. Scan Twitter for the latest signals across crypto/DeFi, AI/tech, and general trends.

Instructions:
1. Run \`birdy home\` to read the home timeline
2. Run \`birdy search "crypto defi"\` for crypto signals
3. Run \`birdy search "AI LLM artificial intelligence"\` for AI signals
4. Run \`birdy news\` for trending topics

Then synthesize your findings into structured sections. Use EXACTLY this format — each section starts with a markdown heading like ## CRYPTO: Title, ## AI: Title, ## TRENDING: Title, ## SIGNAL: Title. Under each heading, write a 1-2 sentence summary paragraph, then a bullet list of key points/accounts. Example:

## CRYPTO: DeFi yields rotating to new L2s
Summary of what's happening in 1-2 sentences with context.
- @account1 noted that...
- Key development: ...
- Signal strength: high

## AI: New model capabilities shipping
Summary paragraph here.
- @account2 announced...
- Notable thread about...

Include 3-6 sections total. Focus on actionable alpha, not noise. If a topic has no meaningful signal, skip it.`;

function readInviteCodeCookie() {
  const cookies = document.cookie ? document.cookie.split('; ') : [];
  const key = `${inviteCodeKey}=`;
  for (const entry of cookies) {
    if (entry.startsWith(key)) return decodeURIComponent(entry.slice(key.length));
  }
  return '';
}

function writeInviteCodeCookie(code: string) {
  const secure = window.location.protocol === 'https:' ? '; Secure' : '';
  document.cookie = `${inviteCodeKey}=${encodeURIComponent(code)}; Path=/; Max-Age=31536000; SameSite=Lax${secure}`;
}

function detectCategory(heading: string): CardCategory {
  const h = heading.toUpperCase();
  if (h.includes('CRYPTO') || h.includes('DEFI') || h.includes('TOKEN') || h.includes('CHAIN')) return 'CRYPTO';
  if (h.includes('AI') || h.includes('LLM') || h.includes('MODEL') || h.includes('TECH')) return 'AI';
  if (h.includes('SIGNAL') || h.includes('ALPHA')) return 'SIGNAL';
  if (h.includes('TRENDING') || h.includes('TREND') || h.includes('NEWS') || h.includes('VIRAL')) return 'TRENDING';
  return 'RESEARCH';
}

function extractSources(text: string): string[] {
  const matches = text.match(/@\w+/g);
  if (!matches) return [];
  return [...new Set(matches)].slice(0, 5);
}

function parseCardsFromMarkdown(markdown: string): AlphaCard[] {
  const cards: AlphaCard[] = [];
  const sections = markdown.split(/^## /m).filter(Boolean);

  for (const section of sections) {
    const lines = section.trim().split('\n');
    if (lines.length === 0) continue;

    const headingLine = lines[0].trim();
    const colonIdx = headingLine.indexOf(':');
    const title = colonIdx >= 0 ? headingLine.slice(colonIdx + 1).trim() : headingLine;
    const category = detectCategory(headingLine);

    const bullets: string[] = [];
    const paragraphs: string[] = [];

    for (let i = 1; i < lines.length; i++) {
      const line = lines[i].trim();
      if (!line) continue;
      const bulletMatch = line.match(/^[-*•]\s+(.+)$/);
      if (bulletMatch) {
        bullets.push(bulletMatch[1]);
      } else if (!/^#{1,4}\s/.test(line)) {
        paragraphs.push(line);
      }
    }

    if (!title && bullets.length === 0 && paragraphs.length === 0) continue;

    const rawMarkdown = `## ${section}`;
    const sources = extractSources(rawMarkdown);

    cards.push({
      id: `card-${Date.now()}-${Math.random().toString(36).slice(2, 8)}`,
      category,
      title: title || 'Signal detected',
      bullets: bullets.length > 0 ? bullets : paragraphs.slice(0, 3),
      sources,
      timestamp: new Date(),
      rawMarkdown,
    });
  }

  return cards;
}

function timeAgo(date: Date): string {
  const seconds = Math.floor((Date.now() - date.getTime()) / 1000);
  if (seconds < 60) return 'just now';
  const minutes = Math.floor(seconds / 60);
  if (minutes < 60) return `${minutes}m ago`;
  const hours = Math.floor(minutes / 60);
  if (hours < 24) return `${hours}h ago`;
  return `${Math.floor(hours / 24)}d ago`;
}

function InvitePanel({
  inviteCode,
  status,
  busy,
  onChange,
  onSubmit,
}: {
  inviteCode: string;
  status: string;
  busy: boolean;
  onChange: (v: string) => void;
  onSubmit: () => void;
}) {
  return (
    <div className="invite-panel">
      <div className="invite-card">
        <h2>Unlock birdy alpha</h2>
        <p className="invite-hint">Enter your invite code to start scanning.</p>
        <input
          type="text"
          autoComplete="off"
          spellCheck={false}
          value={inviteCode}
          placeholder="invite code"
          disabled={busy}
          onChange={(e) => onChange(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === 'Enter') {
              e.preventDefault();
              onSubmit();
            }
          }}
        />
        <button type="button" disabled={busy || !inviteCode.trim()} onClick={onSubmit}>
          {busy ? 'checking...' : 'unlock'}
        </button>
        <p className={`invite-status ${status.toLowerCase().includes('invalid') ? 'error' : ''}`}>{status}</p>
      </div>
    </div>
  );
}

function AlphaCardView({
  card,
  onDeepDive,
}: {
  card: AlphaCard;
  onDeepDive: (card: AlphaCard) => void;
}) {
  const meta = categoryMeta[card.category];
  return (
    <article className={`alpha-card cat-${card.category.toLowerCase()}`}>
      <div className="card-header">
        <span className={`card-tag cat-${card.category.toLowerCase()}`}>
          {meta.icon} {meta.label}
        </span>
        <span className="card-time">{timeAgo(card.timestamp)}</span>
      </div>
      <h3 className="card-title">{card.title}</h3>
      {card.bullets.length > 0 && (
        <ul className="card-bullets">
          {card.bullets.map((b, i) => (
            <li key={i}>{b}</li>
          ))}
        </ul>
      )}
      {card.sources.length > 0 && (
        <div className="card-sources">
          {card.sources.map((s) => (
            <span key={s} className="source-tag">{s}</span>
          ))}
        </div>
      )}
      <button className="card-dive" onClick={() => onDeepDive(card)}>
        Deep Dive
      </button>
    </article>
  );
}

function ScanIndicator({ tools }: { tools: string[] }) {
  return (
    <div className="scan-indicator">
      <div className="scan-pulse" />
      <span className="scan-text">Scanning Twitter...</span>
      {tools.length > 0 && (
        <div className="scan-tools">
          {tools.map((t) => (
            <code key={t} className="tool-chip">{t}</code>
          ))}
        </div>
      )}
    </div>
  );
}

function Composer({
  prompt,
  busy,
  onChange,
  onSend,
}: {
  prompt: string;
  busy: boolean;
  onChange: (v: string) => void;
  onSend: () => void;
}) {
  return (
    <footer className="composer">
      <textarea
        value={prompt}
        disabled={busy}
        placeholder="Ask birdy anything..."
        onChange={(e) => onChange(e.target.value)}
        onKeyDown={(e) => {
          if (e.key === 'Enter' && !e.shiftKey) {
            e.preventDefault();
            onSend();
          }
        }}
      />
      <button type="button" disabled={busy || !prompt.trim()} onClick={onSend}>
        &rarr;
      </button>
    </footer>
  );
}

function ChatBubble({ item }: { item: FeedItem & { kind: 'chat' } }) {
  return (
    <div className={`chat-bubble ${item.role}`}>
      <div className="bubble-label">{item.role === 'user' ? 'You' : 'birdy'}</div>
      <div className="bubble-text">
        {item.loading && !item.text ? 'Thinking...' : item.text || 'No response.'}
      </div>
    </div>
  );
}

export function App() {
  const [inviteCode, setInviteCode] = useState(() => {
    const local = window.localStorage.getItem(inviteCodeKey) || '';
    return local || readInviteCodeCookie();
  });
  const inviteCodeRef = useRef(inviteCode);

  const [authBusy, setAuthBusy] = useState(false);
  const [authStatus, setAuthStatus] = useState('Enter invite code.');
  const [authed, setAuthed] = useState(false);

  const [cards, setCards] = useState<AlphaCard[]>([]);
  const [chatItems, setChatItems] = useState<(FeedItem & { kind: 'chat' })[]>([]);
  const [scanning, setScanning] = useState(false);
  const [scanTools, setScanTools] = useState<string[]>([]);
  const [prompt, setPrompt] = useState('');
  const [genBusy, setGenBusy] = useState(false);

  const streamAbortRef = useRef<AbortController | null>(null);
  const streamRunRef = useRef(0);
  const didAutoAuthRef = useRef(false);
  const feedRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    inviteCodeRef.current = inviteCode;
  }, [inviteCode]);

  const persistInviteCode = useCallback((code: string) => {
    window.localStorage.setItem(inviteCodeKey, code);
    writeInviteCodeCookie(code);
  }, []);

  const streamChat = useCallback(
    async (
      askPrompt: string,
      opts: {
        onToken: (text: string) => void;
        onSnapshot: (text: string) => void;
        onTool: (command: string) => void;
        onDone: (fullText: string) => void;
        onError: (err: string) => void;
        signal: AbortSignal;
      },
    ) => {
      const response = await fetch('/api/chat', {
        method: 'POST',
        headers: {
          'Content-Type': 'application/json',
          'X-Invite-Code': inviteCodeRef.current.trim(),
        },
        body: JSON.stringify({ prompt: askPrompt, model: 'sonnet' }),
        signal: opts.signal,
      });

      if (response.status === 401) {
        setAuthed(false);
        setAuthStatus('Code expired.');
        throw new Error('unauthorized');
      }
      if (!response.ok) {
        const body = await response.text();
        throw new Error(body || `chat failed (${response.status})`);
      }
      if (!response.body) throw new Error('chat stream unavailable');

      const reader = response.body.getReader();
      const decoder = new TextDecoder();
      let buffer = '';
      let fullText = '';

      while (true) {
        const { done, value } = await reader.read();
        if (done) break;

        buffer += decoder.decode(value, { stream: true });
        let split = buffer.indexOf('\n\n');
        while (split >= 0) {
          const block = buffer.slice(0, split);
          buffer = buffer.slice(split + 2);

          let eventName = '';
          let data = '';
          block.split('\n').forEach((line) => {
            if (line.startsWith('event:')) eventName = line.slice(6).trim();
            else if (line.startsWith('data:')) data += line.slice(5).trim();
          });

          if (data) {
            try {
              const payload = JSON.parse(data) as Record<string, unknown>;
              const kind = (typeof payload.type === 'string' && payload.type) || eventName || 'message';

              if (kind === 'snapshot') {
                const text = typeof payload.text === 'string' ? payload.text : '';
                fullText = text;
                opts.onSnapshot(text);
              } else if (kind === 'token') {
                const text = typeof payload.text === 'string' ? payload.text : '';
                if (text) {
                  fullText += text;
                  opts.onToken(text);
                }
              } else if (kind === 'tool_use') {
                const command = typeof payload.command === 'string' ? payload.command.trim() : '';
                if (command) opts.onTool(command);
              } else if (kind === 'error') {
                const text = typeof payload.error === 'string' ? payload.error : 'generation failed';
                opts.onError(text);
              } else if (kind === 'done') {
                opts.onDone(fullText);
              }
            } catch {
              // ignore malformed chunks
            }
          }

          split = buffer.indexOf('\n\n');
        }
      }

      return fullText;
    },
    [],
  );

  const runScan = useCallback(async () => {
    if (scanning || genBusy) return;
    setScanning(true);
    setScanTools([]);

    const controller = new AbortController();
    streamAbortRef.current?.abort();
    streamAbortRef.current = controller;
    const runID = ++streamRunRef.current;

    let accumulated = '';

    try {
      await streamChat(SCAN_PROMPT, {
        signal: controller.signal,
        onToken: (text) => {
          if (streamRunRef.current !== runID) return;
          accumulated += text;
        },
        onSnapshot: (text) => {
          if (streamRunRef.current !== runID) return;
          accumulated = text;
        },
        onTool: (command) => {
          if (streamRunRef.current !== runID) return;
          setScanTools((prev) => (prev.includes(command) ? prev : [...prev, command]));
        },
        onDone: (fullText) => {
          if (streamRunRef.current !== runID) return;
          const parsed = parseCardsFromMarkdown(fullText);
          if (parsed.length > 0) {
            setCards(parsed);
          }
          setScanning(false);
          setScanTools([]);
        },
        onError: () => {
          if (streamRunRef.current !== runID) return;
          // still try to parse what we got
          if (accumulated) {
            const parsed = parseCardsFromMarkdown(accumulated);
            if (parsed.length > 0) setCards(parsed);
          }
          setScanning(false);
          setScanTools([]);
        },
      });
    } catch (err) {
      if (controller.signal.aborted) return;
      if (streamRunRef.current !== runID) return;
      // try to parse partial
      if (accumulated) {
        const parsed = parseCardsFromMarkdown(accumulated);
        if (parsed.length > 0) setCards(parsed);
      }
      setScanning(false);
      setScanTools([]);
    } finally {
      if (streamRunRef.current === runID) {
        setScanning(false);
      }
      if (streamAbortRef.current === controller) streamAbortRef.current = null;
    }
  }, [scanning, genBusy, streamChat]);

  const verifyInviteCode = useCallback(
    async (rawCode?: string) => {
      const code = (rawCode ?? inviteCodeRef.current).trim();
      if (!code) {
        setAuthed(false);
        setAuthStatus('Invite code required.');
        return false;
      }

      setAuthBusy(true);
      setAuthStatus('Checking code...');
      try {
        const response = await fetch('/api/command', {
          method: 'POST',
          headers: {
            'Content-Type': 'application/json',
            'X-Invite-Code': code,
          },
          body: JSON.stringify({ command: 'check' }),
        });

        if (response.status === 401) {
          setAuthed(false);
          setAuthStatus('Invalid code.');
          return false;
        }

        setAuthed(true);
        setAuthStatus('Unlocked.');
        persistInviteCode(code);
        return true;
      } catch {
        setAuthed(false);
        setAuthStatus('Host unreachable.');
        return false;
      } finally {
        setAuthBusy(false);
      }
    },
    [persistInviteCode],
  );

  // Auto-auth on mount
  useEffect(() => {
    if (didAutoAuthRef.current) return;
    didAutoAuthRef.current = true;
    if (!inviteCodeRef.current.trim()) return;
    void verifyInviteCode(inviteCodeRef.current);
  }, [verifyInviteCode]);

  // Auto-scan after auth
  const didAutoScanRef = useRef(false);
  useEffect(() => {
    if (!authed || didAutoScanRef.current) return;
    didAutoScanRef.current = true;
    void runScan();
  }, [authed, runScan]);

  // Cleanup
  useEffect(() => {
    return () => {
      streamAbortRef.current?.abort();
      streamAbortRef.current = null;
    };
  }, []);

  const handleDeepDive = useCallback(
    async (card: AlphaCard) => {
      if (genBusy || scanning) return;
      setGenBusy(true);

      const userItem: FeedItem & { kind: 'chat' } = {
        kind: 'chat',
        id: `u-${Date.now()}`,
        role: 'user',
        text: `Deep dive: ${card.title}`,
        loading: false,
      };
      const assistantId = `a-${Date.now()}`;
      const assistantItem: FeedItem & { kind: 'chat' } = {
        kind: 'chat',
        id: assistantId,
        role: 'assistant',
        text: '',
        loading: true,
      };
      setChatItems((prev) => [...prev, userItem, assistantItem]);

      const controller = new AbortController();
      streamAbortRef.current?.abort();
      streamAbortRef.current = controller;
      const runID = ++streamRunRef.current;

      const deepDivePrompt = `Deep dive into this topic from Twitter: "${card.title}"

Context from initial scan:
${card.rawMarkdown}

Instructions:
1. Search for more details using \`birdy search "${card.title}"\`
2. Look for related threads and discussions
3. Provide a thorough analysis with:
   - What's actually happening
   - Key players and their positions
   - Potential implications
   - Links to relevant tweets/threads if found

Be concise but thorough.`;

      try {
        await streamChat(deepDivePrompt, {
          signal: controller.signal,
          onToken: (text) => {
            if (streamRunRef.current !== runID) return;
            setChatItems((prev) =>
              prev.map((item) =>
                item.id === assistantId ? { ...item, text: item.text + text } : item,
              ),
            );
          },
          onSnapshot: (text) => {
            if (streamRunRef.current !== runID) return;
            setChatItems((prev) =>
              prev.map((item) => (item.id === assistantId ? { ...item, text } : item)),
            );
          },
          onTool: () => {},
          onDone: () => {
            if (streamRunRef.current !== runID) return;
            setChatItems((prev) =>
              prev.map((item) =>
                item.id === assistantId ? { ...item, loading: false } : item,
              ),
            );
          },
          onError: (err) => {
            if (streamRunRef.current !== runID) return;
            setChatItems((prev) =>
              prev.map((item) =>
                item.id === assistantId ? { ...item, text: err || 'Error', loading: false } : item,
              ),
            );
          },
        });
      } catch (err) {
        if (controller.signal.aborted) return;
        if (streamRunRef.current !== runID) return;
        setChatItems((prev) =>
          prev.map((item) =>
            item.id === assistantId
              ? { ...item, text: err instanceof Error ? err.message : 'Request failed', loading: false }
              : item,
          ),
        );
      } finally {
        if (streamRunRef.current === runID) setGenBusy(false);
        if (streamAbortRef.current === controller) streamAbortRef.current = null;
      }
    },
    [genBusy, scanning, streamChat],
  );

  const sendMessage = useCallback(async () => {
    const ask = prompt.trim();
    if (!ask || genBusy || scanning) return;

    setGenBusy(true);
    setPrompt('');

    const userItem: FeedItem & { kind: 'chat' } = {
      kind: 'chat',
      id: `u-${Date.now()}`,
      role: 'user',
      text: ask,
      loading: false,
    };
    const assistantId = `a-${Date.now()}`;
    const assistantItem: FeedItem & { kind: 'chat' } = {
      kind: 'chat',
      id: assistantId,
      role: 'assistant',
      text: '',
      loading: true,
    };
    setChatItems((prev) => [...prev, userItem, assistantItem]);

    const controller = new AbortController();
    streamAbortRef.current?.abort();
    streamAbortRef.current = controller;
    const runID = ++streamRunRef.current;

    try {
      await streamChat(ask, {
        signal: controller.signal,
        onToken: (text) => {
          if (streamRunRef.current !== runID) return;
          setChatItems((prev) =>
            prev.map((item) =>
              item.id === assistantId ? { ...item, text: item.text + text } : item,
            ),
          );
        },
        onSnapshot: (text) => {
          if (streamRunRef.current !== runID) return;
          setChatItems((prev) =>
            prev.map((item) => (item.id === assistantId ? { ...item, text } : item)),
          );
        },
        onTool: () => {},
        onDone: () => {
          if (streamRunRef.current !== runID) return;
          setChatItems((prev) =>
            prev.map((item) =>
              item.id === assistantId ? { ...item, loading: false } : item,
            ),
          );
        },
        onError: (err) => {
          if (streamRunRef.current !== runID) return;
          setChatItems((prev) =>
            prev.map((item) =>
              item.id === assistantId ? { ...item, text: err || 'Error', loading: false } : item,
            ),
          );
        },
      });
    } catch (err) {
      if (controller.signal.aborted) return;
      if (streamRunRef.current !== runID) return;
      setChatItems((prev) =>
        prev.map((item) =>
          item.id === assistantId
            ? { ...item, text: err instanceof Error ? err.message : 'Request failed', loading: false }
            : item,
        ),
      );
    } finally {
      if (streamRunRef.current === runID) setGenBusy(false);
      if (streamAbortRef.current === controller) streamAbortRef.current = null;
    }
  }, [prompt, genBusy, scanning, streamChat]);

  // Auto-scroll feed
  useEffect(() => {
    if (feedRef.current) {
      feedRef.current.scrollTop = feedRef.current.scrollHeight;
    }
  }, [cards, chatItems, scanning]);

  if (!authed) {
    return (
      <div className="shell">
        <header className="header">
          <div className="brand">
            <h1>birdy alpha</h1>
          </div>
          <span className="badge idle">{authBusy ? 'checking' : 'locked'}</span>
        </header>
        <InvitePanel
          inviteCode={inviteCode}
          status={authStatus}
          busy={authBusy}
          onChange={setInviteCode}
          onSubmit={() => void verifyInviteCode(inviteCodeRef.current)}
        />
      </div>
    );
  }

  return (
    <div className="shell">
      <header className="header">
        <div className="brand">
          <h1>birdy alpha</h1>
        </div>
        <div className="header-actions">
          <span className="badge live">live</span>
          <button
            className="scan-btn"
            disabled={scanning || genBusy}
            onClick={() => void runScan()}
            title="Refresh scan"
          >
            {scanning ? '\u23F3' : '\u21BB'}
          </button>
        </div>
      </header>

      <main className="feed" ref={feedRef}>
        {scanning && <ScanIndicator tools={scanTools} />}

        {!scanning && cards.length === 0 && chatItems.length === 0 && (
          <div className="empty-state">
            <p>No signals yet. Scan starting...</p>
          </div>
        )}

        {cards.map((card) => (
          <AlphaCardView key={card.id} card={card} onDeepDive={handleDeepDive} />
        ))}

        {chatItems.map((item) => (
          <ChatBubble key={item.id} item={item} />
        ))}
      </main>

      <Composer
        prompt={prompt}
        busy={genBusy || scanning}
        onChange={setPrompt}
        onSend={() => void sendMessage()}
      />
    </div>
  );
}
