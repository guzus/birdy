import { Fragment, useCallback, useEffect, useRef, useState } from 'react';
import type { ReactNode } from 'react';
import { parseMarkdownBlocks } from './markdown';

const inviteCodeKey = 'birdy_host_invite_code';
const conversationsKey = 'birdy_conversations';

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

type Conversation = {
  id: string;
  title: string;
  chatItems: (FeedItem & { kind: 'chat' })[];
  cards: AlphaCard[];
  createdAt: number;
  updatedAt: number;
};

type RunningKind = 'chat' | 'scan';

function makeConvId() {
  return `conv-${Date.now()}-${Math.random().toString(36).slice(2, 8)}`;
}

function loadConversations(): Conversation[] {
  try {
    const raw = localStorage.getItem(conversationsKey);
    if (raw) {
      return (JSON.parse(raw) as Conversation[]).map(c => ({
        ...c,
        cards: c.cards.map(card => ({ ...card, timestamp: new Date(card.timestamp) })),
        chatItems: c.chatItems.map(item => item.loading ? { ...item, loading: false } : item),
      }));
    }
    // Migrate legacy sessionStorage data
    const oldCards = sessionStorage.getItem('birdy_cards');
    const oldChat = sessionStorage.getItem('birdy_chat_items');
    if (oldCards || oldChat) {
      const cards = oldCards
        ? (JSON.parse(oldCards) as AlphaCard[]).map(c => ({ ...c, timestamp: new Date(c.timestamp) }))
        : [];
      const chatItems = oldChat
        ? (JSON.parse(oldChat) as (FeedItem & { kind: 'chat' })[]).map(i => i.loading ? { ...i, loading: false } : i)
        : [];
      sessionStorage.removeItem('birdy_cards');
      sessionStorage.removeItem('birdy_chat_items');
      if (cards.length > 0 || chatItems.length > 0) {
        const firstMsg = chatItems.find(i => i.role === 'user');
        return [{
          id: makeConvId(), title: firstMsg?.text.slice(0, 50) ?? 'Imported chat',
          chatItems, cards, createdAt: Date.now(), updatedAt: Date.now(),
        }];
      }
    }
  } catch {}
  return [];
}

const categoryMeta: Record<CardCategory, { label: string }> = {
  CRYPTO: { label: 'Crypto' },
  AI: { label: 'AI' },
  TRENDING: { label: 'Trending' },
  SIGNAL: { label: 'Signal' },
  RESEARCH: { label: 'Research' },
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

type TrackerAccount = {
  handle: string;
  name: string;
  bio: string;
  following: string;
  followers: string;
  joined: string;
  avatar: string;
  banner: string;
  accent: string;
};

type FollowSignal = {
  id: string;
  follower: TrackerAccount;
  followed: TrackerAccount;
  confidence: 'hypothesis' | 'confirmed';
  detectedAt: string;
  thesis: string;
  tags: string[];
};

const trackerQueue = ['@sama', '@karpathy', '@levelsio', '@rauchg', '@amasad'];

const trackerSignals: FollowSignal[] = [
  {
    id: 'signal-elon-kilo',
    confidence: 'hypothesis',
    detectedAt: '2026-05-25 01:04',
    thesis: 'Elon Musk follows Kilo, an open-source AI coding platform, possibly for its agentic engineering workflow and local tooling angle.',
    tags: ['AI coding', 'open source', 'developer tools'],
    follower: {
      handle: '@elonmusk',
      name: 'Elon Musk',
      bio: 'Founder, builder, product signal amplifier.',
      following: '1.3K',
      followers: '240.0M',
      joined: 'June, 2009',
      avatar: 'EM',
      banner: 'LAUNCH',
      accent: '#111111',
    },
    followed: {
      handle: '@kilocode',
      name: 'Kilo',
      bio: 'Open source AI coding platform for agentic engineering teams.',
      following: '200',
      followers: '20.0K',
      joined: 'March, 2025',
      avatar: 'K',
      banner: 'AI for everything',
      accent: '#fff84a',
    },
  },
  {
    id: 'signal-sama-browser',
    confidence: 'confirmed',
    detectedAt: '2026-05-24 22:18',
    thesis: 'A frontier-lab leader followed a small browser automation project after several engineering threads gained traction.',
    tags: ['agents', 'browser', 'automation'],
    follower: {
      handle: '@sama',
      name: 'Sam Altman',
      bio: 'AI lab operator tracking fast-moving software primitives.',
      following: '3.4K',
      followers: '5.1M',
      joined: 'July, 2009',
      avatar: 'SA',
      banner: 'LAB',
      accent: '#0f766e',
    },
    followed: {
      handle: '@browserbasehq',
      name: 'Browserbase',
      bio: 'Cloud browsers for AI agents and automation workloads.',
      following: '480',
      followers: '54.2K',
      joined: 'May, 2023',
      avatar: 'BB',
      banner: 'BROWSER AGENTS',
      accent: '#1d4ed8',
    },
  },
];

function TrackerAccountCard({ account, align = 'left' }: { account: TrackerAccount; align?: 'left' | 'right' }) {
  return (
    <section className="min-w-0 bg-white text-[#111]">
      <div
        className="h-20 sm:h-24 border-b border-black/10 flex items-center px-4"
        style={{ background: account.accent }}
      >
        <div className={`w-full text-[10px] sm:text-xs font-mono font-bold uppercase tracking-[0.12em] ${account.accent === '#fff84a' ? 'text-black' : 'text-white'} ${align === 'right' ? 'text-right' : ''}`}>
          {account.banner}
        </div>
      </div>
      <div className="px-4 pb-4">
        <div className="-mt-10 mb-3">
          <div className={`w-16 h-16 sm:w-20 sm:h-20 rounded-md border-4 border-white shadow-sm flex items-center justify-center font-black text-xl ${account.accent === '#fff84a' ? 'bg-[#fff84a] text-black' : 'bg-slate-900 text-white'}`}>
            {account.avatar}
          </div>
        </div>
        <div className="font-mono text-[11px] text-[#666]">{account.handle}</div>
        <h3 className="m-0 text-xl sm:text-2xl font-bold leading-none">{account.name}</h3>
        <div className="mt-1 flex items-center gap-1.5 text-[11px] text-[#4b86d9]">
          <span className="w-2 h-2 rounded-full bg-[#3b82f6]" />
          <span>Premium</span>
        </div>
        <div className="mt-4 grid grid-cols-2 gap-3 max-w-[180px]">
          <div>
            <div className="font-mono text-sm font-bold leading-none">{account.following}</div>
            <div className="text-[10px] text-[#777]">Following</div>
          </div>
          <div>
            <div className="font-mono text-sm font-bold leading-none">{account.followers}</div>
            <div className="text-[10px] text-[#777]">Followers</div>
          </div>
        </div>
        <p className="mt-4 mb-8 text-[11px] sm:text-xs leading-snug text-[#333] min-h-[40px]">{account.bio}</p>
        <div className="text-[10px] text-[#777]">Joined: {account.joined}</div>
      </div>
    </section>
  );
}

function FollowSignalCard({ signal }: { signal: FollowSignal }) {
  return (
    <article className="overflow-hidden rounded-lg border border-black bg-white shadow-sm">
      <div className="bg-[#1f9d55] text-white text-center font-mono text-[11px] font-bold tracking-[0.18em] uppercase py-2">
        New follow
      </div>
      <div className="grid grid-cols-1 sm:grid-cols-[1fr_2px_1fr]">
        <TrackerAccountCard account={signal.follower} />
        <div className="hidden sm:block bg-black" />
        <TrackerAccountCard account={signal.followed} align="right" />
      </div>
      <div className="bg-black text-white px-4 py-2 flex items-center justify-between gap-3 text-[10px] font-mono">
        <span>Network signal detected</span>
        <span className="text-white/70">{signal.detectedAt}</span>
        <span className="hidden sm:inline text-white/70">powered by birdy</span>
      </div>
    </article>
  );
}

function TrackerDemo() {
  const primarySignal = trackerSignals[0];
  return (
    <div className="min-h-full bg-[#f6f7f8] text-[#111]">
      <main className="mx-auto max-w-[1120px] px-4 py-5 sm:px-6 sm:py-8">
        <div className="grid gap-5 lg:grid-cols-[minmax(0,1fr)_320px]">
          <section className="min-w-0">
            <div className="rounded-lg border border-black/10 bg-white px-4 py-4 sm:px-6 sm:py-5">
              <div className="flex items-center gap-3">
                <div className="w-10 h-10 rounded-full bg-[#081826] text-white flex items-center justify-center font-black text-sm">B</div>
                <div>
                  <div className="font-semibold leading-tight">@AI followgraph <span className="rounded bg-[#178f36] px-1 py-0.5 text-[10px] text-white align-middle">ON</span></div>
                  <div className="text-xs text-[#667085]">Monitoring high-signal follows across AI builders</div>
                </div>
              </div>
              <div className="mt-5 space-y-4 text-[15px] sm:text-lg leading-relaxed">
                <p className="m-0">NEW <span className="text-[#0a84ff]">{primarySignal.follower.handle}</span> has started following <span className="text-[#0a84ff]">{primarySignal.followed.handle}</span></p>
                <p className="m-0">Pure hypothesis, not confirmed: {primarySignal.thesis}</p>
                <p className="m-0">Comment AI leaders below that we should add to our monitor queue.</p>
              </div>
              <div className="mt-4 flex flex-wrap gap-2">
                {primarySignal.tags.map((tag) => (
                  <span key={tag} className="rounded-full bg-[#eef2f7] px-3 py-1 text-xs text-[#344054]">{tag}</span>
                ))}
              </div>
            </div>

            <div className="mt-4">
              <FollowSignalCard signal={primarySignal} />
            </div>
          </section>

          <aside className="space-y-4">
            <section className="rounded-lg border border-black/10 bg-white p-4">
              <div className="flex items-center justify-between">
                <h2 className="m-0 text-sm font-semibold">Monitor queue</h2>
                <span className="font-mono text-[11px] text-[#667085]">{trackerQueue.length} targets</span>
              </div>
              <div className="mt-3 flex flex-col gap-2">
                {trackerQueue.map((handle, index) => (
                  <div key={handle} className="flex items-center justify-between rounded-md border border-black/10 px-3 py-2">
                    <span className="font-mono text-sm">{handle}</span>
                    <span className="text-[11px] text-[#667085]">{index === 0 ? 'hot' : 'watching'}</span>
                  </div>
                ))}
              </div>
            </section>

            <section className="rounded-lg border border-black/10 bg-white p-4">
              <h2 className="m-0 text-sm font-semibold">Recent signals</h2>
              <div className="mt-3 flex flex-col gap-3">
                {trackerSignals.map((signal) => (
                  <div key={signal.id} className="border-b border-black/10 pb-3 last:border-b-0 last:pb-0">
                    <div className="font-mono text-[11px] uppercase text-[#667085]">{signal.confidence}</div>
                    <div className="mt-1 text-sm font-medium">{signal.follower.handle} {'->'} {signal.followed.handle}</div>
                    <div className="mt-1 text-xs leading-relaxed text-[#667085]">{signal.thesis}</div>
                  </div>
                ))}
              </div>
            </section>

            <section className="rounded-lg border border-black/10 bg-[#081826] p-4 text-white">
              <h2 className="m-0 text-sm font-semibold">Tracker contract</h2>
              <dl className="mt-3 grid grid-cols-2 gap-3 text-xs">
                <div>
                  <dt className="text-white/50">Poll</dt>
                  <dd className="m-0 font-mono">15m</dd>
                </div>
                <div>
                  <dt className="text-white/50">Alert</dt>
                  <dd className="m-0 font-mono">new edge</dd>
                </div>
                <div>
                  <dt className="text-white/50">State</dt>
                  <dd className="m-0 font-mono">dedupe</dd>
                </div>
                <div>
                  <dt className="text-white/50">Output</dt>
                  <dd className="m-0 font-mono">tweet card</dd>
                </div>
              </dl>
            </section>
          </aside>
        </div>
      </main>
    </div>
  );
}

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

function renderInlineMarkdown(text: string, keyPrefix: string): ReactNode[] {
  const nodes: ReactNode[] = [];
  let rest = text;
  let part = 0;

  while (rest) {
    const patterns: Array<{
      kind: 'code' | 'link' | 'x-link' | 'strong';
      match: RegExpExecArray | null;
    }> = [
      { kind: 'code', match: /`([^`\n]+)`/.exec(rest) },
      { kind: 'link', match: /\[([^\]]+)\]\((https?:\/\/[^\s)]+)\)/.exec(rest) },
      {
        kind: 'x-link',
        match: /(?<![\w./-])(?:https?:\/\/)?(?:www\.)?x\.com\/[^\s<>()\[\]{}"'`]*[A-Za-z0-9_/#?=&%-]/i.exec(rest),
      },
      { kind: 'strong', match: /\*\*([^*][\s\S]*?)\*\*/.exec(rest) },
    ].filter((entry) => entry.match);

    if (patterns.length === 0) {
      nodes.push(rest);
      break;
    }

    patterns.sort((a, b) => (a.match?.index ?? 0) - (b.match?.index ?? 0));
    const next = patterns[0];
    const match = next.match!;
    const index = match.index ?? 0;

    if (index > 0) nodes.push(rest.slice(0, index));

    const key = `${keyPrefix}-${part++}`;
    if (next.kind === 'code') {
      nodes.push(
        <code key={key} className="font-mono text-[0.92em] px-1.5 py-0.5 rounded bg-bg-2 text-text">
          {match[1]}
        </code>,
      );
    } else if (next.kind === 'link') {
      nodes.push(
        <a
          key={key}
          href={match[2]}
          target="_blank"
          rel="noreferrer"
          className="text-text underline decoration-text-dim/40 underline-offset-2 break-all"
        >
          {match[1]}
        </a>,
      );
    } else if (next.kind === 'x-link') {
      const href = /^https?:\/\//i.test(match[0]) ? match[0] : `https://${match[0]}`;
      nodes.push(
        <a
          key={key}
          href={href}
          target="_blank"
          rel="noreferrer"
          className="text-text underline decoration-text-dim/40 underline-offset-2 break-all"
        >
          {match[0]}
        </a>,
      );
    } else {
      nodes.push(<strong key={key} className="font-semibold text-text">{renderInlineMarkdown(match[1], key)}</strong>);
    }

    rest = rest.slice(index + match[0].length);
  }

  return nodes;
}

export function MarkdownMessage({ text }: { text: string }) {
  const blocks = parseMarkdownBlocks(text);
  if (blocks.length === 0) return <span>No response.</span>;

  return (
    <div className="flex flex-col gap-3 text-sm leading-normal text-text-muted break-words">
      {blocks.map((block, blockIdx) => {
        const key = `md-${blockIdx}`;

        if (block.kind === 'heading') {
          const headingClass =
            block.level === 1
              ? 'text-xl font-semibold text-text'
              : block.level === 2
                ? 'text-lg font-semibold text-text'
                : 'text-base font-semibold text-text';
          return (
            <div key={key} className={headingClass}>
              {renderInlineMarkdown(block.text, `${key}-heading`)}
            </div>
          );
        }

        if (block.kind === 'paragraph') {
          return (
            <p key={key} className="m-0">
              {block.lines.map((line, lineIdx) => (
                <Fragment key={`${key}-line-${lineIdx}`}>
                  {renderInlineMarkdown(line, `${key}-line-${lineIdx}`)}
                  {lineIdx < block.lines.length - 1 && <br />}
                </Fragment>
              ))}
            </p>
          );
        }

        if (block.kind === 'ul') {
          return (
            <ul key={key} className="m-0 pl-5 flex flex-col gap-1.5">
              {block.items.map((item, itemIdx) => (
                <li key={`${key}-item-${itemIdx}`}>{renderInlineMarkdown(item, `${key}-item-${itemIdx}`)}</li>
              ))}
            </ul>
          );
        }

        if (block.kind === 'ol') {
          return (
            <ol key={key} className="m-0 pl-5 flex flex-col gap-1.5 list-decimal">
              {block.items.map((item, itemIdx) => (
                <li key={`${key}-item-${itemIdx}`}>{renderInlineMarkdown(item, `${key}-item-${itemIdx}`)}</li>
              ))}
            </ol>
          );
        }

        if (block.kind === 'table') {
          return (
            <div key={key} className="w-full overflow-x-auto rounded-lg border border-border">
              <table className="w-max min-w-full border-collapse text-left text-sm">
                <thead className="bg-bg-2 text-text">
                  <tr>
                    {block.headers.map((header, columnIdx) => (
                      <th
                        key={`${key}-header-${columnIdx}`}
                        scope="col"
                        className="whitespace-nowrap border-b border-border px-3 py-2 font-semibold"
                        style={{ textAlign: block.alignments[columnIdx] ?? undefined }}
                      >
                        {renderInlineMarkdown(header, `${key}-header-${columnIdx}`)}
                      </th>
                    ))}
                  </tr>
                </thead>
                <tbody>
                  {block.rows.map((row, rowIdx) => (
                    <tr key={`${key}-row-${rowIdx}`} className="border-b border-border/50 last:border-b-0">
                      {row.map((cell, columnIdx) => (
                        <td
                          key={`${key}-row-${rowIdx}-cell-${columnIdx}`}
                          className="px-3 py-2 align-top"
                          style={{ textAlign: block.alignments[columnIdx] ?? undefined }}
                        >
                          {renderInlineMarkdown(cell, `${key}-row-${rowIdx}-cell-${columnIdx}`)}
                        </td>
                      ))}
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          );
        }

        if (block.kind === 'code') {
          return (
            <pre key={key} className="m-0 overflow-x-auto rounded-lg bg-bg-2 px-3 py-2 text-[12px] leading-relaxed text-text">
              <code>{block.code}</code>
            </pre>
          );
        }

        return <hr key={key} className="border-0 border-t border-border my-1" />;
      })}
    </div>
  );
}

function BirdyWordmark() {
  return (
    <h1 className="m-0 flex items-center gap-2 text-lg font-semibold tracking-tight text-text">
      <img src="/birdy-logo.svg" alt="" aria-hidden="true" className="h-7 w-7 shrink-0 object-contain" />
      <span>birdy</span>
    </h1>
  );
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
    <div className="flex items-center justify-center min-h-0 flex-1">
      <div className="w-full max-w-[380px] border border-border rounded-lg p-6 flex flex-col gap-3">
        <h2 className="m-0 text-lg font-semibold text-text">birdy</h2>
        <p className="m-0 text-[13px] text-text-muted">Enter your invite code.</p>
        <input
          type="text"
          autoComplete="off"
          spellCheck={false}
          value={inviteCode}
          placeholder="invite code"
          disabled={busy}
          className="w-full bg-transparent border border-border rounded-lg text-text font-[inherit] text-sm py-2.5 px-3 outline-none focus:border-text placeholder:text-text-dim"
          onChange={(e) => onChange(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === 'Enter') {
              e.preventDefault();
              onSubmit();
            }
          }}
        />
        <button
          type="button"
          disabled={busy || !inviteCode.trim()}
          className="self-start bg-text text-bg border-none rounded-lg font-[inherit] text-xs font-medium py-2.5 px-5 cursor-pointer disabled:opacity-40 disabled:cursor-not-allowed"
          onClick={onSubmit}
        >
          {busy ? 'checking...' : 'unlock'}
        </button>
        <p className={`m-0 text-xs ${status.toLowerCase().includes('invalid') ? 'text-danger' : 'text-text-muted'}`}>
          {status}
        </p>
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
    <article className="border-b border-border py-4 sm:py-5 flex flex-col gap-2">
      <div className="flex items-center gap-2 text-[11px] text-text-dim">
        <span className="uppercase tracking-wide font-medium">{meta.label}</span>
        <span>&middot;</span>
        <span className="font-mono">{timeAgo(card.timestamp)}</span>
      </div>
      <h3 className="m-0 text-base font-semibold leading-snug text-text">{card.title}</h3>
      {card.bullets.length > 0 && (
        <ul className="m-0 pl-4 flex flex-col gap-0.5">
          {card.bullets.map((b, i) => (
            <li key={i} className="text-[13px] leading-relaxed text-text-muted">{b}</li>
          ))}
        </ul>
      )}
      {card.sources.length > 0 && (
        <div className="flex flex-wrap gap-1.5">
          {card.sources.map((s) => (
            <span key={s} className="font-mono text-[11px] text-text-dim">
              {s}
            </span>
          ))}
        </div>
      )}
      <button
        className="self-start text-text-dim text-xs font-medium underline underline-offset-2 cursor-pointer font-[inherit] bg-transparent border-none p-0 hover:text-text transition-colors"
        onClick={() => onDeepDive(card)}
      >
        Deep dive
      </button>
    </article>
  );
}

function sanitizeError(raw: string): string {
  if (!raw) return 'Something went wrong.';
  if (/<[a-z][\s\S]*>/i.test(raw)) return 'Request failed — server returned an error.';
  const trimmed = raw.trim();
  if (trimmed.length > 200) return trimmed.slice(0, 200) + '\u2026';
  return trimmed;
}

function ScanIndicator({ tools, onCancel }: { tools: string[]; onCancel: () => void }) {
  return (
    <div className="py-8 flex flex-col items-center gap-3">
      <div className="w-2 h-2 rounded-full bg-text-dim animate-pulse-dot" />
      <span className="text-[13px] text-text-dim">Scanning...</span>
      {tools.length > 0 && (
        <div className="flex flex-wrap gap-1.5 justify-center">
          {tools.map((t) => (
            <code key={t} className="font-mono text-[10px] py-0.5 px-1.5 text-text-dim">
              {t}
            </code>
          ))}
        </div>
      )}
      <button
        onClick={onCancel}
        className="text-[12px] text-text-dim underline underline-offset-2 bg-transparent border-none cursor-pointer font-[inherit] hover:text-text"
      >
        Cancel
      </button>
    </div>
  );
}

function Composer({
  prompt,
  busy,
  onChange,
  onSend,
  onStop,
}: {
  prompt: string;
  busy: boolean;
  onChange: (v: string) => void;
  onSend: () => void;
  onStop?: () => void;
}) {
  const isMobile = typeof window !== 'undefined' && window.matchMedia('(pointer: coarse)').matches;

  return (
    <footer className="grid grid-cols-[minmax(0,1fr)_auto] gap-2 items-end border-t border-border pt-2 sm:pt-3 px-1">
      <textarea
        value={prompt}
        placeholder={busy ? 'Type to queue next message\u2026' : 'Ask anything...'}
        className="bg-transparent border border-border rounded-lg text-text font-[inherit] text-sm py-2.5 px-3 min-h-[44px] max-h-[120px] sm:max-h-[150px] resize-none sm:resize-y outline-none leading-snug w-full focus:border-text placeholder:text-text-dim"
        onChange={(e) => onChange(e.target.value)}
        onKeyDown={(e) => {
          if (e.key === 'Enter' && !e.shiftKey && !isMobile) {
            e.preventDefault();
            onSend();
          }
        }}
      />
      <div className="flex flex-col gap-1.5">
        {busy && onStop && (
          <button
            type="button"
            className="w-10 h-10 rounded-lg border border-border text-danger text-sm font-bold cursor-pointer flex items-center justify-center hover:bg-bg-2 transition-colors"
            onClick={onStop}
            title="Stop"
          >
            &#9632;
          </button>
        )}
        <button
          type="button"
          disabled={!prompt.trim()}
          className="w-10 h-10 rounded-lg border border-border bg-text text-bg text-lg font-bold cursor-pointer flex items-center justify-center transition-opacity duration-150 disabled:opacity-20 disabled:cursor-not-allowed"
          onClick={onSend}
        >
          &rarr;
        </button>
      </div>
    </footer>
  );
}

function ChatBubble({ item }: { item: FeedItem & { kind: 'chat' } }) {
  const text = item.loading && !item.text ? 'Thinking...' : item.text || 'No response.';
  return (
    <div className={`border-b border-border py-3 sm:py-4 flex flex-col gap-1 ${item.role === 'user' ? 'bg-accent-light rounded' : ''}`}>
      <div className={`text-[11px] font-medium uppercase tracking-wide ${item.role === 'user' ? 'text-text' : 'text-text-dim'}`}>
        {item.role === 'user' ? 'You' : 'birdy'}
      </div>
      <div className={item.role === 'user' ? 'text-sm leading-relaxed whitespace-pre-wrap break-words text-text' : ''}>
        {item.role === 'user' ? text : <MarkdownMessage text={text} />}
      </div>
    </div>
  );
}

export function App() {
  const showTrackerDemo = new URLSearchParams(window.location.search).get('demo') === 'tracker';
  const [inviteCode, setInviteCode] = useState(() => {
    const local = window.localStorage.getItem(inviteCodeKey) || '';
    return local || readInviteCodeCookie();
  });
  const inviteCodeRef = useRef(inviteCode);

  const [authBusy, setAuthBusy] = useState(false);
  const [authStatus, setAuthStatus] = useState('Enter invite code.');
  const [authed, setAuthed] = useState(false);

  const [conversations, setConversations] = useState<Conversation[]>(loadConversations);
  const [activeConvId, setActiveConvId] = useState<string | null>(() => {
    const convs = loadConversations();
    return convs.length > 0 ? convs[0].id : null;
  });
  const [sidebarOpen, setSidebarOpen] = useState(false);
  const [prompt, setPrompt] = useState('');
  const [runningByConv, setRunningByConv] = useState<Record<string, RunningKind>>({});
  const [scanToolsByConv, setScanToolsByConv] = useState<Record<string, string[]>>({});

  const activeConv = conversations.find(c => c.id === activeConvId) ?? null;
  const cards = activeConv?.cards ?? [];
  const chatItems = activeConv?.chatItems ?? [];
  const activeRunning = activeConvId ? runningByConv[activeConvId] : undefined;
  const scanning = activeRunning === 'scan';
  const genBusy = activeRunning === 'chat';
  const scanTools = activeConvId ? (scanToolsByConv[activeConvId] ?? []) : [];

  const updateConv = useCallback((id: string, fn: (c: Conversation) => Conversation) => {
    setConversations(prev => prev.map(c => c.id === id ? fn(c) : c));
  }, []);

  const ensureConv = useCallback((): string => {
    if (activeConvId) return activeConvId;
    const newConv: Conversation = {
      id: makeConvId(), title: 'New chat', chatItems: [], cards: [],
      createdAt: Date.now(), updatedAt: Date.now(),
    };
    setConversations(prev => [newConv, ...prev]);
    setActiveConvId(newConv.id);
    return newConv.id;
  }, [activeConvId]);

  const streamAbortRefs = useRef(new Map<string, AbortController>());
  const didAutoAuthRef = useRef(false);
  const feedRef = useRef<HTMLDivElement>(null);
  const queuedPromptRef = useRef<{ convId: string; prompt: string } | null>(null);
  const doSendRef = useRef<((ask: string, convId?: string) => void) | undefined>(undefined);

  const setConvRunning = useCallback((id: string, kind: RunningKind) => {
    setRunningByConv(prev => ({ ...prev, [id]: kind }));
  }, []);

  const clearConvRunning = useCallback((id: string) => {
    setRunningByConv(prev => {
      if (!prev[id]) return prev;
      const next = { ...prev };
      delete next[id];
      return next;
    });
  }, []);

  const clearConvScanTools = useCallback((id: string) => {
    setScanToolsByConv(prev => {
      if (!prev[id]) return prev;
      const next = { ...prev };
      delete next[id];
      return next;
    });
  }, []);

  const finishConvStream = useCallback((id: string, controller: AbortController) => {
    if (streamAbortRefs.current.get(id) !== controller) return;
    streamAbortRefs.current.delete(id);
    clearConvRunning(id);
    clearConvScanTools(id);
  }, [clearConvRunning, clearConvScanTools]);

  const abortConvStream = useCallback((id: string) => {
    const controller = streamAbortRefs.current.get(id);
    controller?.abort();
    streamAbortRefs.current.delete(id);
    clearConvRunning(id);
    clearConvScanTools(id);
  }, [clearConvRunning, clearConvScanTools]);

  useEffect(() => {
    inviteCodeRef.current = inviteCode;
  }, [inviteCode]);

  useEffect(() => {
    try { localStorage.setItem(conversationsKey, JSON.stringify(conversations)); } catch {}
  }, [conversations]);

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
    const convId = ensureConv();
    abortConvStream(convId);
    setConvRunning(convId, 'scan');
    clearConvScanTools(convId);

    const controller = new AbortController();
    streamAbortRefs.current.set(convId, controller);

    let accumulated = '';

    const applyCards = (parsed: AlphaCard[]) => {
      if (parsed.length > 0) updateConv(convId, c => ({ ...c, cards: parsed, updatedAt: Date.now() }));
    };

    try {
      await streamChat(SCAN_PROMPT, {
        signal: controller.signal,
        onToken: (text) => {
          if (controller.signal.aborted) return;
          accumulated += text;
        },
        onSnapshot: (text) => {
          if (controller.signal.aborted) return;
          accumulated = text;
        },
        onTool: (command) => {
          if (controller.signal.aborted) return;
          setScanToolsByConv((prev) => {
            const tools = prev[convId] ?? [];
            if (tools.includes(command)) return prev;
            return { ...prev, [convId]: [...tools, command] };
          });
        },
        onDone: (fullText) => {
          if (controller.signal.aborted) return;
          applyCards(parseCardsFromMarkdown(fullText));
          finishConvStream(convId, controller);
        },
        onError: () => {
          if (controller.signal.aborted) return;
          if (accumulated) applyCards(parseCardsFromMarkdown(accumulated));
          finishConvStream(convId, controller);
        },
      });
    } catch (err) {
      if (controller.signal.aborted) return;
      if (accumulated) applyCards(parseCardsFromMarkdown(accumulated));
      finishConvStream(convId, controller);
    } finally {
      finishConvStream(convId, controller);
    }
  }, [scanning, genBusy, streamChat, ensureConv, updateConv, abortConvStream, setConvRunning, clearConvScanTools, finishConvStream]);

  const cancelScan = useCallback(() => {
    if (!activeConvId) return;
    abortConvStream(activeConvId);
  }, [activeConvId, abortConvStream]);

  const cancelChat = useCallback(() => {
    if (!activeConvId) return;
    abortConvStream(activeConvId);
    updateConv(activeConvId, c => ({
        ...c,
        chatItems: c.chatItems.map(item =>
          item.loading ? { ...item, loading: false, text: item.text || 'Cancelled.' } : item,
        ),
        updatedAt: Date.now(),
    }));
  }, [activeConvId, updateConv, abortConvStream]);

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

  useEffect(() => {
    if (didAutoAuthRef.current) return;
    didAutoAuthRef.current = true;
    if (!inviteCodeRef.current.trim()) return;
    void verifyInviteCode(inviteCodeRef.current);
  }, [verifyInviteCode]);

  // No auto-scan — user triggers it manually via CTA or Refresh button.

  useEffect(() => {
    return () => {
      streamAbortRefs.current.forEach(controller => controller.abort());
      streamAbortRefs.current.clear();
    };
  }, []);

  const handleDeepDive = useCallback(
    async (card: AlphaCard) => {
      if (genBusy || scanning) return;
      const convId = ensureConv();
      abortConvStream(convId);
      setConvRunning(convId, 'chat');

      const userItem: FeedItem & { kind: 'chat' } = {
        kind: 'chat', id: `u-${Date.now()}`, role: 'user',
        text: `Deep dive: ${card.title}`, loading: false,
      };
      const assistantId = `a-${Date.now()}`;
      const assistantItem: FeedItem & { kind: 'chat' } = {
        kind: 'chat', id: assistantId, role: 'assistant', text: '', loading: true,
      };
      updateConv(convId, c => ({
        ...c,
        chatItems: [...c.chatItems, userItem, assistantItem],
        title: c.chatItems.some(i => i.role === 'user') ? c.title : `Deep dive: ${card.title}`.slice(0, 50),
        updatedAt: Date.now(),
      }));

      const controller = new AbortController();
      streamAbortRefs.current.set(convId, controller);

      const mapItem = (fn: (item: FeedItem & { kind: 'chat' }) => FeedItem & { kind: 'chat' }) =>
        updateConv(convId, c => ({ ...c, chatItems: c.chatItems.map(i => i.id === assistantId ? fn(i) : i), updatedAt: Date.now() }));

      const deepDivePrompt = `Deep dive into this topic from Twitter: "${card.title}"\n\nContext from initial scan:\n${card.rawMarkdown}\n\nInstructions:\n1. Search for more details using \`birdy search "${card.title}"\`\n2. Look for related threads and discussions\n3. Provide a thorough analysis with:\n   - What's actually happening\n   - Key players and their positions\n   - Potential implications\n   - Links to relevant tweets/threads if found\n\nBe concise but thorough.`;

      try {
        await streamChat(deepDivePrompt, {
          signal: controller.signal,
          onToken: (text) => { if (controller.signal.aborted) return; mapItem(i => ({ ...i, text: i.text + text })); },
          onSnapshot: (text) => { if (controller.signal.aborted) return; mapItem(i => ({ ...i, text })); },
          onTool: () => {},
          onDone: () => { if (controller.signal.aborted) return; mapItem(i => ({ ...i, loading: false })); finishConvStream(convId, controller); },
          onError: (err) => { if (controller.signal.aborted) return; mapItem(i => ({ ...i, text: sanitizeError(err), loading: false })); finishConvStream(convId, controller); },
        });
      } catch (err) {
        if (controller.signal.aborted) return;
        mapItem(i => ({ ...i, text: sanitizeError(err instanceof Error ? err.message : 'Request failed'), loading: false }));
        finishConvStream(convId, controller);
      } finally {
        finishConvStream(convId, controller);
      }
    },
    [genBusy, scanning, streamChat, ensureConv, updateConv, abortConvStream, setConvRunning, finishConvStream],
  );

  const buildChatPrompt = useCallback((newMessage: string, convId: string): string => {
    const history = (conversations.find(c => c.id === convId)?.chatItems ?? [])
      .filter((item) => item.text && !item.loading)
      .slice(-10);
    if (history.length === 0) return newMessage;
    const lines = history.map((item) =>
      `${item.role === 'user' ? 'User' : 'Assistant'}: ${item.text}`,
    ).join('\n\n');
    return `Conversation so far:\n${lines}\n\nUser: ${newMessage}\n\nContinue the conversation. Respond to the latest message using the context above.`;
  }, [conversations]);

  const sendMessage = useCallback(async (overridePrompt?: string, targetConvId?: string) => {
    const ask = (overridePrompt ?? prompt).trim();
    if (!ask) return;

    const convId = targetConvId ?? ensureConv();
    if (runningByConv[convId]) {
      queuedPromptRef.current = { convId, prompt: ask };
      if (!overridePrompt) setPrompt('');
      return;
    }
    setConvRunning(convId, 'chat');
    if (!overridePrompt) setPrompt('');

    const contextPrompt = buildChatPrompt(ask, convId);

    const userItem: FeedItem & { kind: 'chat' } = {
      kind: 'chat', id: `u-${Date.now()}`, role: 'user', text: ask, loading: false,
    };
    const assistantId = `a-${Date.now()}`;
    const assistantItem: FeedItem & { kind: 'chat' } = {
      kind: 'chat', id: assistantId, role: 'assistant', text: '', loading: true,
    };
    updateConv(convId, c => ({
      ...c,
      chatItems: [...c.chatItems, userItem, assistantItem],
      title: c.chatItems.some(i => i.role === 'user') ? c.title : ask.slice(0, 50),
      updatedAt: Date.now(),
    }));

    const mapItem = (fn: (item: FeedItem & { kind: 'chat' }) => FeedItem & { kind: 'chat' }) =>
      updateConv(convId, c => ({ ...c, chatItems: c.chatItems.map(i => i.id === assistantId ? fn(i) : i), updatedAt: Date.now() }));

    const controller = new AbortController();
    streamAbortRefs.current.set(convId, controller);

    try {
      await streamChat(contextPrompt, {
        signal: controller.signal,
        onToken: (text) => { if (controller.signal.aborted) return; mapItem(i => ({ ...i, text: i.text + text })); },
        onSnapshot: (text) => { if (controller.signal.aborted) return; mapItem(i => ({ ...i, text })); },
        onTool: () => {},
        onDone: () => { if (controller.signal.aborted) return; mapItem(i => ({ ...i, loading: false })); finishConvStream(convId, controller); },
        onError: (err) => { if (controller.signal.aborted) return; mapItem(i => ({ ...i, text: sanitizeError(err), loading: false })); finishConvStream(convId, controller); },
      });
    } catch (err) {
      if (controller.signal.aborted) return;
      mapItem(i => ({ ...i, text: sanitizeError(err instanceof Error ? err.message : 'Request failed'), loading: false }));
      finishConvStream(convId, controller);
    } finally {
      finishConvStream(convId, controller);
    }
  }, [prompt, streamChat, buildChatPrompt, ensureConv, updateConv, runningByConv, setConvRunning, finishConvStream]);

  doSendRef.current = (ask: string, convId?: string) => void sendMessage(ask, convId);

  useEffect(() => {
    const queued = queuedPromptRef.current;
    if (!queued) return;
    if (runningByConv[queued.convId]) return;
    queuedPromptRef.current = null;
    doSendRef.current?.(queued.prompt, queued.convId);
  }, [runningByConv]);

  useEffect(() => {
    if (feedRef.current) {
      feedRef.current.scrollTop = feedRef.current.scrollHeight;
    }
  }, [cards, chatItems, scanning]);

  const handleNewChat = useCallback(() => {
    const newConv: Conversation = {
      id: makeConvId(), title: 'New chat', chatItems: [], cards: [],
      createdAt: Date.now(), updatedAt: Date.now(),
    };
    setConversations(prev => [newConv, ...prev]);
    setActiveConvId(newConv.id);
    setSidebarOpen(false);
  }, []);

  const switchConv = useCallback((id: string) => {
    setActiveConvId(id);
    setSidebarOpen(false);
  }, []);

  const deleteConv = useCallback((id: string) => {
    abortConvStream(id);
    if (queuedPromptRef.current?.convId === id) queuedPromptRef.current = null;
    setConversations(prev => {
      const next = prev.filter(c => c.id !== id);
      if (id === activeConvId) {
        setActiveConvId(next.length > 0 ? next[0].id : null);
      }
      return next;
    });
  }, [activeConvId, abortConvStream]);

  if (showTrackerDemo) {
    return <TrackerDemo />;
  }

  if (!authed) {
    return (
      <div className="h-full max-w-[640px] mx-auto grid grid-rows-[auto_minmax(0,1fr)] p-3 sm:p-4 gap-0">
        <header className="flex items-center justify-between py-3 border-b border-border">
          <BirdyWordmark />
          <span className="text-[11px] text-text-dim">
            {authBusy ? 'checking...' : ''}
          </span>
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
    <div className="h-full flex">
      {/* Sidebar */}
      <aside className={`fixed inset-y-0 left-0 z-30 w-[260px] bg-bg border-r border-border flex flex-col transition-transform duration-200 md:relative md:translate-x-0 ${sidebarOpen ? 'translate-x-0' : '-translate-x-full'}`}>
        <div className="flex items-center justify-between p-3 border-b border-border">
          <span className="text-sm font-semibold text-text">Chats</span>
          <button onClick={handleNewChat} className="text-xs text-text-dim hover:text-text bg-transparent border-none cursor-pointer font-[inherit]">+ New</button>
        </div>
        <div className="flex-1 overflow-y-auto hide-scrollbar">
          {conversations.map(conv => (
            <div
              key={conv.id}
              className={`group flex items-center cursor-pointer border-b border-border/30 ${conv.id === activeConvId ? 'bg-accent-light' : 'hover:bg-bg-2/50'}`}
            >
              <button
                onClick={() => switchConv(conv.id)}
                className="flex-1 text-left px-3 py-2.5 flex flex-col gap-0.5 bg-transparent border-none cursor-pointer font-[inherit] min-w-0"
              >
                <span className="text-sm text-text truncate block">{conv.title}</span>
                <span className="text-[10px] text-text-dim font-mono">{timeAgo(new Date(conv.updatedAt))}</span>
              </button>
              <button
                onClick={(e) => { e.stopPropagation(); deleteConv(conv.id); }}
                className="hidden group-hover:block px-2 text-text-dim hover:text-danger text-xs bg-transparent border-none cursor-pointer"
              >
                &times;
              </button>
            </div>
          ))}
          {conversations.length === 0 && (
            <p className="text-text-dim text-xs p-3 m-0">No conversations yet.</p>
          )}
        </div>
      </aside>

      {/* Mobile backdrop */}
      {sidebarOpen && (
        <div className="fixed inset-0 z-20 bg-black/20 md:hidden" onClick={() => setSidebarOpen(false)} />
      )}

      {/* Main content */}
      <div className="flex-1 min-w-0 h-full flex flex-col">
        {/* Desktop promo banner */}
        <div className="hidden md:flex items-center justify-end gap-4 px-4 py-1.5 text-[11px] text-text-dim border-b border-border/50">
          <a href="https://memtherscan.xyz" target="_blank" rel="noreferrer" className="hover:text-text transition-colors no-underline text-text-dim">memtherscan.xyz &mdash; Monitor trending memes</a>
          <a href="https://polymarket.com/?r=s3xy" target="_blank" rel="noreferrer" className="hover:text-text transition-colors no-underline text-text-dim">Polymarket &mdash; Bet on anything</a>
        </div>
      <div className="flex-1 min-w-0 max-w-[640px] mx-auto w-full grid grid-rows-[auto_minmax(0,1fr)_auto] p-3 sm:p-4 gap-0">
        <header className="flex items-center justify-between py-3 border-b border-border">
          <div className="flex items-center gap-3">
            <button
              className="md:hidden text-text text-lg bg-transparent border-none cursor-pointer p-0 leading-none"
              onClick={() => setSidebarOpen(o => !o)}
            >
              &#9776;
            </button>
            <BirdyWordmark />
          </div>
          <div className="flex items-center gap-4"></div>
        </header>

        <main className="min-h-0 overflow-y-auto flex flex-col py-1 hide-scrollbar" ref={feedRef}>
          {scanning && <ScanIndicator tools={scanTools} onCancel={cancelScan} />}

          {!scanning && cards.length === 0 && chatItems.length === 0 && (
            <div className="flex flex-col items-center justify-center h-[300px] gap-4">
              <p className="m-0 text-text-dim text-sm">Start a conversation or scan your timeline.</p>
              <button
                className="bg-text text-bg border-none rounded-lg font-[inherit] text-sm font-medium py-2.5 px-6 cursor-pointer hover:opacity-80 transition-opacity disabled:opacity-30 disabled:cursor-not-allowed"
                disabled={genBusy}
                onClick={() => void runScan()}
              >
                Scan Timeline
              </button>
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
          onStop={genBusy ? cancelChat : scanning ? cancelScan : undefined}
        />
      </div>
      </div>
    </div>
  );
}
