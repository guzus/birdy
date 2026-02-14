import { useCallback, useEffect, useMemo, useRef, useState } from 'react';

import { defineCatalog, type Spec } from '@json-render/core';
import { JSONUIProvider, Renderer, defineRegistry, schema } from '@json-render/react';
import { z } from 'zod';

const inviteCodeKey = 'birdy_host_invite_code';

type ParsedResultBlock =
  | { kind: 'heading'; text: string; level: 2 | 3 | 4 }
  | { kind: 'list'; ordered: boolean; items: string[] }
  | { kind: 'paragraph'; text: string };

type ChatMessage = {
  id: string;
  role: 'user' | 'assistant';
  text: string;
  tools: string[];
  error: string;
  loading: boolean;
};

function readInviteCodeCookie() {
  const cookies = document.cookie ? document.cookie.split('; ') : [];
  const key = `${inviteCodeKey}=`;
  for (const entry of cookies) {
    if (entry.startsWith(key)) {
      return decodeURIComponent(entry.slice(key.length));
    }
  }
  return '';
}

function writeInviteCodeCookie(code: string) {
  const secure = window.location.protocol === 'https:' ? '; Secure' : '';
  document.cookie = `${inviteCodeKey}=${encodeURIComponent(
    code,
  )}; Path=/; Max-Age=31536000; SameSite=Lax${secure}`;
}

function normalizeError(err: unknown) {
  if (err instanceof Error && err.message.trim()) return err.message;
  if (typeof err === 'string' && err.trim()) return err;
  return 'request failed';
}

function parseResultBlocks(input: string): ParsedResultBlock[] {
  const lines = input.replace(/\r\n/g, '\n').split('\n');
  const blocks: ParsedResultBlock[] = [];
  let i = 0;

  while (i < lines.length) {
    const raw = lines[i].trim();
    if (!raw) {
      i++;
      continue;
    }

    const headingMatch = raw.match(/^(#{1,4})\s+(.+)$/);
    if (headingMatch) {
      const level = Math.min(4, Math.max(2, headingMatch[1].length + 1)) as 2 | 3 | 4;
      blocks.push({ kind: 'heading', level, text: headingMatch[2].trim() });
      i++;
      continue;
    }

    const boldHeading = raw.match(/^\*\*(.+?)\*\*:?\s*$/);
    if (boldHeading) {
      blocks.push({ kind: 'heading', level: 3, text: boldHeading[1].trim() });
      i++;
      continue;
    }

    if (/^[-*•]\s+/.test(raw)) {
      const items: string[] = [];
      while (i < lines.length) {
        const m = lines[i].trim().match(/^[-*•]\s+(.+)$/);
        if (!m) break;
        items.push(m[1].trim());
        i++;
      }
      if (items.length > 0) blocks.push({ kind: 'list', ordered: false, items });
      continue;
    }

    if (/^\d+\.\s+/.test(raw)) {
      const items: string[] = [];
      while (i < lines.length) {
        const m = lines[i].trim().match(/^\d+\.\s+(.+)$/);
        if (!m) break;
        items.push(m[1].trim());
        i++;
      }
      if (items.length > 0) blocks.push({ kind: 'list', ordered: true, items });
      continue;
    }

    const paragraph: string[] = [];
    while (i < lines.length) {
      const current = lines[i].trim();
      if (!current) {
        i++;
        if (paragraph.length > 0) break;
        continue;
      }
      if (
        /^(#{1,4})\s+/.test(current) ||
        /^\*\*(.+?)\*\*:?\s*$/.test(current) ||
        /^[-*•]\s+/.test(current) ||
        /^\d+\.\s+/.test(current)
      ) {
        break;
      }
      paragraph.push(current);
      i++;
    }
    if (paragraph.length > 0) blocks.push({ kind: 'paragraph', text: paragraph.join('\n') });
  }

  return blocks;
}

const catalog = defineCatalog(schema, {
  components: {
    Shell: {
      props: z.object({}),
      description: 'App shell',
    },
    Header: {
      props: z.object({}),
      description: 'Top bar',
    },
    Brand: {
      props: z.object({
        title: z.string(),
        subtitle: z.string(),
      }),
      description: 'Brand block',
    },
    Badge: {
      props: z.object({
        text: z.string(),
        tone: z.enum(['live', 'idle']),
      }),
      description: 'Connection badge',
    },
    Main: {
      props: z.object({}),
      description: 'Main layout',
    },
    InvitePanel: {
      props: z.object({
        inviteCode: z.string(),
        status: z.string(),
        busy: z.boolean(),
        onChange: z.any(),
        onSubmit: z.any(),
      }),
      description: 'Invite form',
    },
    ChatFeed: {
      props: z.object({}),
      description: 'Message list container',
    },
    Composer: {
      props: z.object({
        prompt: z.string(),
        busy: z.boolean(),
        onChange: z.any(),
        onSend: z.any(),
      }),
      description: 'Prompt composer',
    },
    UserBubble: {
      props: z.object({
        text: z.string(),
      }),
      description: 'User message bubble',
    },
    AssistantBubble: {
      props: z.object({}),
      description: 'Assistant message bubble',
    },
    ResultStatus: {
      props: z.object({
        text: z.string(),
        tone: z.enum(['info', 'error']),
      }),
      description: 'Status line',
    },
    ToolsRow: {
      props: z.object({}),
      description: 'Tool command row',
    },
    ToolChip: {
      props: z.object({
        command: z.string(),
      }),
      description: 'Tool command badge',
    },
    ResultHeading: {
      props: z.object({
        text: z.string(),
        level: z.number().int().min(2).max(4),
      }),
      description: 'Assistant heading',
    },
    ResultList: {
      props: z.object({
        ordered: z.boolean(),
      }),
      description: 'Assistant list',
    },
    ResultListItem: {
      props: z.object({
        text: z.string(),
      }),
      description: 'Assistant list item',
    },
    ResultText: {
      props: z.object({
        text: z.string(),
      }),
      description: 'Assistant paragraph',
    },
    EmptyHint: {
      props: z.object({
        text: z.string(),
      }),
      description: 'Empty state hint',
    },
  },
  actions: {},
});

const { registry } = defineRegistry(catalog, {
  components: {
    Shell: ({ children }) => <div className="gpt-shell">{children}</div>,
    Header: ({ children }) => <header className="gpt-header">{children}</header>,
    Brand: ({ props }) => (
      <div className="gpt-brand">
        <h1>{props.title}</h1>
        <p>{props.subtitle}</p>
      </div>
    ),
    Badge: ({ props }) => <span className={`gpt-badge tone-${props.tone}`}>{props.text}</span>,
    Main: ({ children }) => <main className="gpt-main">{children}</main>,
    InvitePanel: ({ props }) => {
      const onChange = props.onChange as (value: string) => void;
      const onSubmit = props.onSubmit as () => void;
      return (
        <section className="gpt-invite">
          <h2>Unlock</h2>
          <p>Enter invite code.</p>
          <label htmlFor="invite-code" className="sr-only">
            Invite code
          </label>
          <input
            id="invite-code"
            type="text"
            autoComplete="off"
            spellCheck={false}
            value={props.inviteCode}
            placeholder="invite code"
            disabled={props.busy}
            onChange={(event) => onChange(event.target.value)}
            onKeyDown={(event) => {
              if (event.key === 'Enter') {
                event.preventDefault();
                onSubmit();
              }
            }}
          />
          <button
            type="button"
            disabled={props.busy || props.inviteCode.trim().length === 0}
            onClick={onSubmit}
          >
            {props.busy ? 'checking...' : 'unlock'}
          </button>
          <p className={`gpt-invite-status ${props.status.toLowerCase().includes('invalid') ? 'tone-error' : ''}`}>
            {props.status}
          </p>
        </section>
      );
    },
    ChatFeed: ({ children }) => <section className="gpt-feed">{children}</section>,
    Composer: ({ props }) => {
      const onChange = props.onChange as (value: string) => void;
      const onSend = props.onSend as () => void;
      return (
        <footer className="gpt-composer">
          <label htmlFor="prompt-input" className="sr-only">
            Prompt
          </label>
          <textarea
            id="prompt-input"
            value={props.prompt}
            disabled={props.busy}
            placeholder="Message birdy..."
            onChange={(event) => onChange(event.target.value)}
            onKeyDown={(event) => {
              if (event.key === 'Enter' && !event.shiftKey) {
                event.preventDefault();
                onSend();
              }
            }}
          />
          <button
            type="button"
            disabled={props.busy || props.prompt.trim().length === 0}
            onClick={onSend}
          >
            {props.busy ? '...' : 'send'}
          </button>
        </footer>
      );
    },
    UserBubble: ({ props }) => (
      <article className="gpt-row user">
        <div className="gpt-bubble user">{props.text}</div>
      </article>
    ),
    AssistantBubble: ({ children }) => (
      <article className="gpt-row assistant">
        <div className="gpt-bubble assistant">{children}</div>
      </article>
    ),
    ResultStatus: ({ props }) => <p className={`gpt-status tone-${props.tone}`}>{props.text}</p>,
    ToolsRow: ({ children }) => <div className="gpt-tools">{children}</div>,
    ToolChip: ({ props }) => <code className="gpt-tool">{props.command}</code>,
    ResultHeading: ({ props }) => {
      const headingLevel = props.level <= 2 ? 2 : props.level === 3 ? 3 : 4;
      const Tag = `h${headingLevel}` as 'h2' | 'h3' | 'h4';
      return <Tag className={`gpt-heading level-${headingLevel}`}>{props.text}</Tag>;
    },
    ResultList: ({ props, children }) =>
      props.ordered ? <ol className="gpt-list ordered">{children}</ol> : <ul className="gpt-list">{children}</ul>,
    ResultListItem: ({ props }) => <li>{props.text}</li>,
    ResultText: ({ props }) => <p className="gpt-text">{props.text}</p>,
    EmptyHint: ({ props }) => <p className="gpt-empty">{props.text}</p>,
  },
});

function newID(prefix: string) {
  return `${prefix}-${Date.now()}-${Math.random().toString(36).slice(2, 8)}`;
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

  const [prompt, setPrompt] = useState('');
  const [messages, setMessages] = useState<ChatMessage[]>([]);
  const [genBusy, setGenBusy] = useState(false);

  const streamAbortRef = useRef<AbortController | null>(null);
  const streamRunRef = useRef(0);
  const didAutoAuthRef = useRef(false);

  useEffect(() => {
    inviteCodeRef.current = inviteCode;
  }, [inviteCode]);

  const persistInviteCode = useCallback((code: string) => {
    window.localStorage.setItem(inviteCodeKey, code);
    writeInviteCodeCookie(code);
  }, []);

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

  useEffect(() => {
    return () => {
      streamAbortRef.current?.abort();
      streamAbortRef.current = null;
    };
  }, []);

  const updateAssistant = useCallback((id: string, updater: (msg: ChatMessage) => ChatMessage) => {
    setMessages((prev) => prev.map((msg) => (msg.id === id ? updater(msg) : msg)));
  }, []);

  const sendMessage = useCallback(async () => {
    const ask = prompt.trim();
    if (!ask || genBusy) return;

    if (!authed) {
      const ok = await verifyInviteCode();
      if (!ok) return;
    }

    const userID = newID('u');
    const assistantID = newID('a');
    setMessages((prev) => [
      ...prev,
      { id: userID, role: 'user', text: ask, tools: [], error: '', loading: false },
      { id: assistantID, role: 'assistant', text: '', tools: [], error: '', loading: true },
    ]);
    setPrompt('');
    setGenBusy(true);

    streamAbortRef.current?.abort();
    const controller = new AbortController();
    streamAbortRef.current = controller;
    const runID = ++streamRunRef.current;

    try {
      const response = await fetch('/api/chat', {
        method: 'POST',
        headers: {
          'Content-Type': 'application/json',
          'X-Invite-Code': inviteCodeRef.current.trim(),
        },
        body: JSON.stringify({ prompt: ask, model: 'sonnet' }),
        signal: controller.signal,
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
      if (!response.body) {
        throw new Error('chat stream unavailable');
      }

      const reader = response.body.getReader();
      const decoder = new TextDecoder();
      let buffer = '';

      const handleEvent = (kind: string, payload: Record<string, unknown>) => {
        if (streamRunRef.current !== runID) return;
        if (kind === 'snapshot') {
          const text = typeof payload.text === 'string' ? payload.text : '';
          updateAssistant(assistantID, (msg) => ({ ...msg, text }));
          return;
        }
        if (kind === 'token') {
          const text = typeof payload.text === 'string' ? payload.text : '';
          if (text) {
            updateAssistant(assistantID, (msg) => ({ ...msg, text: msg.text + text }));
          }
          return;
        }
        if (kind === 'tool_use') {
          const command = typeof payload.command === 'string' ? payload.command.trim() : '';
          if (command) {
            updateAssistant(assistantID, (msg) => ({
              ...msg,
              tools: msg.tools.includes(command) ? msg.tools : [...msg.tools, command],
            }));
          }
          return;
        }
        if (kind === 'error') {
          const text = typeof payload.error === 'string' ? payload.error : '';
          updateAssistant(assistantID, (msg) => ({ ...msg, error: text || 'generation failed', loading: false }));
          return;
        }
        if (kind === 'done') {
          updateAssistant(assistantID, (msg) => ({ ...msg, loading: false }));
        }
      };

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
              const kind =
                (typeof payload.type === 'string' && payload.type) || eventName || 'message';
              handleEvent(kind, payload);
            } catch {
              // Ignore malformed chunks.
            }
          }

          split = buffer.indexOf('\n\n');
        }
      }
    } catch (err) {
      if (controller.signal.aborted) return;
      if (streamRunRef.current !== runID) return;
      if (normalizeError(err) !== 'unauthorized') {
        updateAssistant(assistantID, (msg) => ({
          ...msg,
          loading: false,
          error: normalizeError(err),
        }));
      }
    } finally {
      if (streamRunRef.current === runID) setGenBusy(false);
      if (streamAbortRef.current === controller) streamAbortRef.current = null;
    }
  }, [authed, genBusy, prompt, updateAssistant, verifyInviteCode]);

  const uiSpec = useMemo<Spec>(() => {
    const elements: Spec['elements'] = {
      shell: { type: 'Shell', props: {}, children: ['header', 'main'] },
      header: { type: 'Header', props: {}, children: ['brand', 'badge'] },
      brand: {
        type: 'Brand',
        props: {
          title: 'birdy',
          subtitle: 'AI assistant for X/Twitter actions and analysis',
        },
        children: [],
      },
      badge: {
        type: 'Badge',
        props: {
          text: authed ? 'live' : authBusy ? 'checking' : 'locked',
          tone: authed ? 'live' : 'idle',
        },
        children: [],
      },
      main: { type: 'Main', props: {}, children: [] },
    };

    if (!authed) {
      elements.main.children = ['invite'];
      elements.invite = {
        type: 'InvitePanel',
        props: {
          inviteCode,
          status: authStatus,
          busy: authBusy,
          onChange: setInviteCode,
          onSubmit: () => {
            void verifyInviteCode(inviteCodeRef.current);
          },
        },
        children: [],
      };
      return { root: 'shell', elements };
    }

    elements.main.children = ['feed', 'composer'];
    elements.feed = { type: 'ChatFeed', props: {}, children: [] };
    elements.composer = {
      type: 'Composer',
      props: {
        prompt,
        busy: genBusy,
        onChange: setPrompt,
        onSend: () => {
          void sendMessage();
        },
      },
      children: [],
    };

    const feedChildren: string[] = [];

    if (messages.length === 0) {
      const emptyKey = 'empty';
      feedChildren.push(emptyKey);
      elements[emptyKey] = {
        type: 'EmptyHint',
        props: { text: 'Start chatting with birdy.' },
        children: [],
      };
    } else {
      messages.forEach((msg, msgIndex) => {
        if (msg.role === 'user') {
          const key = `msg-user-${msg.id}`;
          feedChildren.push(key);
          elements[key] = { type: 'UserBubble', props: { text: msg.text }, children: [] };
          return;
        }

        const assistantKey = `msg-assistant-${msg.id}`;
        const assistantChildren: string[] = [];
        elements[assistantKey] = { type: 'AssistantBubble', props: {}, children: assistantChildren };
        feedChildren.push(assistantKey);

        if (msg.error) {
          const k = `${assistantKey}-error`;
          assistantChildren.push(k);
          elements[k] = { type: 'ResultStatus', props: { text: msg.error, tone: 'error' }, children: [] };
        } else if (msg.loading && !msg.text) {
          const k = `${assistantKey}-loading`;
          assistantChildren.push(k);
          elements[k] = { type: 'ResultStatus', props: { text: 'Thinking...', tone: 'info' }, children: [] };
        }

        if (msg.tools.length > 0) {
          const row = `${assistantKey}-tools`;
          const toolChildren = msg.tools.map((_, i) => `${row}-${i}`);
          assistantChildren.push(row);
          elements[row] = { type: 'ToolsRow', props: {}, children: toolChildren };
          msg.tools.forEach((command, i) => {
            elements[`${row}-${i}`] = { type: 'ToolChip', props: { command }, children: [] };
          });
        }

        const text = msg.text.trim();
        if (text) {
          parseResultBlocks(text).forEach((block, blockIndex) => {
            if (block.kind === 'paragraph') {
              const key = `${assistantKey}-text-${blockIndex}`;
              assistantChildren.push(key);
              elements[key] = { type: 'ResultText', props: { text: block.text }, children: [] };
              return;
            }
            if (block.kind === 'heading') {
              const key = `${assistantKey}-heading-${blockIndex}`;
              assistantChildren.push(key);
              elements[key] = {
                type: 'ResultHeading',
                props: { text: block.text, level: block.level },
                children: [],
              };
              return;
            }
            const listKey = `${assistantKey}-list-${blockIndex}`;
            const listChildren = block.items.map((_, i) => `${listKey}-item-${i}`);
            assistantChildren.push(listKey);
            elements[listKey] = {
              type: 'ResultList',
              props: { ordered: block.ordered },
              children: listChildren,
            };
            block.items.forEach((item, itemIndex) => {
              elements[`${listKey}-item-${itemIndex}`] = {
                type: 'ResultListItem',
                props: { text: item },
                children: [],
              };
            });
          });
        } else if (!msg.loading && !msg.error) {
          const key = `${assistantKey}-empty`;
          assistantChildren.push(key);
          elements[key] = { type: 'EmptyHint', props: { text: 'No response.' }, children: [] };
        }

        // Preserve stable ordering by ensuring bubble keys are deterministic.
        if (msgIndex > 999999) {
          // no-op to keep lints calm about msgIndex usage if needed by future changes
        }
      });
    }

    elements.feed.children = feedChildren;
    return { root: 'shell', elements };
  }, [authBusy, authStatus, authed, genBusy, inviteCode, messages, prompt, sendMessage, verifyInviteCode]);

  return (
    <JSONUIProvider registry={registry}>
      <Renderer spec={uiSpec} registry={registry} />
    </JSONUIProvider>
  );
}
