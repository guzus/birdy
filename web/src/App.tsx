import { useCallback, useEffect, useMemo, useRef, useState } from 'react';

import { defineCatalog, type Spec } from '@json-render/core';
import { JSONUIProvider, Renderer, defineRegistry, schema } from '@json-render/react';
import { z } from 'zod';

const inviteCodeKey = 'birdy_host_invite_code';

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

type ParsedResultBlock =
  | { kind: 'heading'; text: string; level: 2 | 3 | 4 }
  | { kind: 'list'; ordered: boolean; items: string[] }
  | { kind: 'paragraph'; text: string };

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

    const unorderedStart = raw.match(/^[-*•]\s+(.+)$/);
    if (unorderedStart) {
      const items: string[] = [];
      while (i < lines.length) {
        const m = lines[i].trim().match(/^[-*•]\s+(.+)$/);
        if (!m) break;
        items.push(m[1].trim());
        i++;
      }
      if (items.length > 0) {
        blocks.push({ kind: 'list', ordered: false, items });
      }
      continue;
    }

    const orderedStart = raw.match(/^\d+\.\s+(.+)$/);
    if (orderedStart) {
      const items: string[] = [];
      while (i < lines.length) {
        const m = lines[i].trim().match(/^\d+\.\s+(.+)$/);
        if (!m) break;
        items.push(m[1].trim());
        i++;
      }
      if (items.length > 0) {
        blocks.push({ kind: 'list', ordered: true, items });
      }
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
    if (paragraph.length > 0) {
      blocks.push({ kind: 'paragraph', text: paragraph.join('\n') });
    }
  }

  return blocks;
}

const catalog = defineCatalog(schema, {
  components: {
    Shell: {
      props: z.object({}),
      description: 'Application shell',
    },
    Header: {
      props: z.object({}),
      description: 'Header row',
    },
    Brand: {
      props: z.object({
        eyebrow: z.string(),
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
    Workspace: {
      props: z.object({}),
      description: 'Main two-panel workspace',
    },
    Panel: {
      props: z.object({
        title: z.string(),
        subtitle: z.string(),
      }),
      description: 'Panel container',
    },
    InviteForm: {
      props: z.object({
        inviteCode: z.string(),
        status: z.string(),
        busy: z.boolean(),
        onChange: z.any(),
        onSubmit: z.any(),
      }),
      description: 'Invite code form',
    },
    PromptForm: {
      props: z.object({
        prompt: z.string(),
        busy: z.boolean(),
        canSubmit: z.boolean(),
        canClear: z.boolean(),
        onChange: z.any(),
        onSubmit: z.any(),
        onClear: z.any(),
      }),
      description: 'Generative prompt form',
    },
    ResultStatus: {
      props: z.object({
        text: z.string(),
        tone: z.enum(['info', 'error']),
      }),
      description: 'Result status text',
    },
    ToolsRow: {
      props: z.object({}),
      description: 'Tool command row',
    },
    ToolChip: {
      props: z.object({
        command: z.string(),
      }),
      description: 'Single tool command',
    },
    ResultText: {
      props: z.object({
        text: z.string(),
      }),
      description: 'Result text block',
    },
    ResultHeading: {
      props: z.object({
        text: z.string(),
        level: z.number().int().min(2).max(4),
      }),
      description: 'Result section heading',
    },
    ResultList: {
      props: z.object({
        ordered: z.boolean(),
      }),
      description: 'Result list container',
    },
    ResultListItem: {
      props: z.object({
        text: z.string(),
      }),
      description: 'Result list item',
    },
    EmptyHint: {
      props: z.object({
        text: z.string(),
      }),
      description: 'Result empty hint',
    },
  },
  actions: {},
});

const { registry } = defineRegistry(catalog, {
  components: {
    Shell: ({ children }) => <div className="jw-shell">{children}</div>,
    Header: ({ children }) => <header className="jw-header">{children}</header>,
    Brand: ({ props }) => (
      <div className="jw-brand">
        {props.eyebrow ? <p className="jw-brand-eyebrow">{props.eyebrow}</p> : null}
        <h1 className="jw-brand-title">{props.title}</h1>
        <p className="jw-brand-subtitle">{props.subtitle}</p>
      </div>
    ),
    Badge: ({ props }) => <span className={`jw-badge tone-${props.tone}`}>{props.text}</span>,
    Workspace: ({ children }) => <main className="jw-workspace">{children}</main>,
    Panel: ({ props, children }) => (
      <section className="jw-panel">
        <div className="jw-panel-head">
          <h2>{props.title}</h2>
          <p>{props.subtitle}</p>
        </div>
        <div className="jw-panel-body">{children}</div>
      </section>
    ),
    InviteForm: ({ props }) => {
      const onChange = props.onChange as (value: string) => void;
      const onSubmit = props.onSubmit as () => void;
      return (
        <div className="jw-invite-form">
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
            {props.busy ? 'verifying...' : 'unlock'}
          </button>
          <p className={`jw-form-status ${props.status.toLowerCase().includes('invalid') ? 'tone-error' : ''}`}>
            {props.status}
          </p>
        </div>
      );
    },
    PromptForm: ({ props }) => {
      const onChange = props.onChange as (value: string) => void;
      const onSubmit = props.onSubmit as () => void;
      const onClear = props.onClear as () => void;
      return (
        <div className="jw-prompt-form">
          <label htmlFor="prompt-input" className="sr-only">
            Prompt
          </label>
          <textarea
            id="prompt-input"
            value={props.prompt}
            disabled={props.busy}
            placeholder="Ask for analysis or an action plan."
            onChange={(event) => onChange(event.target.value)}
            onKeyDown={(event) => {
              if ((event.ctrlKey || event.metaKey) && event.key === 'Enter') {
                event.preventDefault();
                onSubmit();
              }
            }}
          />
          <div className="jw-prompt-actions">
            <button type="button" disabled={!props.canSubmit} onClick={onSubmit}>
              {props.busy ? 'generating...' : 'generate'}
            </button>
            <button className="secondary" type="button" disabled={!props.canClear} onClick={onClear}>
              clear
            </button>
          </div>
        </div>
      );
    },
    ResultStatus: ({ props }) => <p className={`jw-result-status tone-${props.tone}`}>{props.text}</p>,
    ToolsRow: ({ children }) => <div className="jw-tools-row">{children}</div>,
    ToolChip: ({ props }) => <code className="jw-tool-chip">{props.command}</code>,
    ResultHeading: ({ props }) => {
      const headingLevel = props.level <= 2 ? 2 : props.level === 3 ? 3 : 4;
      const Tag = `h${headingLevel}` as 'h2' | 'h3' | 'h4';
      return <Tag className={`jw-result-heading level-${headingLevel}`}>{props.text}</Tag>;
    },
    ResultList: ({ props, children }) =>
      props.ordered ? (
        <ol className="jw-result-list ordered">{children}</ol>
      ) : (
        <ul className="jw-result-list unordered">{children}</ul>
      ),
    ResultListItem: ({ props }) => <li className="jw-result-item">{props.text}</li>,
    ResultText: ({ props }) => <p className="jw-result-text">{props.text}</p>,
    EmptyHint: ({ props }) => <p className="jw-empty-hint">{props.text}</p>,
  },
});

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
  const [genLoading, setGenLoading] = useState(false);
  const [genError, setGenError] = useState('');
  const [genText, setGenText] = useState('');
  const [genTools, setGenTools] = useState<string[]>([]);

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

  const runGenerative = useCallback(async () => {
    const ask = prompt.trim();
    if (!ask) return;

    if (!authed) {
      const ok = await verifyInviteCode();
      if (!ok) return;
    }

    streamAbortRef.current?.abort();
    const controller = new AbortController();
    streamAbortRef.current = controller;
    const runID = ++streamRunRef.current;

    setGenLoading(true);
    setGenError('');
    setGenText('');
    setGenTools([]);

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
          setGenText(text);
          return;
        }
        if (kind === 'token') {
          const text = typeof payload.text === 'string' ? payload.text : '';
          if (text) setGenText((prev) => prev + text);
          return;
        }
        if (kind === 'tool_use') {
          const command = typeof payload.command === 'string' ? payload.command.trim() : '';
          if (command) {
            setGenTools((prev) => (prev.includes(command) ? prev : [...prev, command]));
          }
          return;
        }
        if (kind === 'error') {
          const text = typeof payload.error === 'string' ? payload.error : '';
          setGenError(text || 'generation failed');
          return;
        }
        if (kind === 'done') {
          setGenLoading(false);
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
        setGenError(normalizeError(err));
      }
    } finally {
      if (streamRunRef.current === runID) setGenLoading(false);
      if (streamAbortRef.current === controller) streamAbortRef.current = null;
    }
  }, [authed, prompt, verifyInviteCode]);

  const clearOutput = useCallback(() => {
    streamAbortRef.current?.abort();
    streamAbortRef.current = null;
    setGenLoading(false);
    setGenError('');
    setGenText('');
    setGenTools([]);
  }, []);

  const uiSpec = useMemo<Spec>(() => {
    const elements: Spec['elements'] = {
      shell: { type: 'Shell', props: {}, children: ['header', 'workspace'] },
      header: { type: 'Header', props: {}, children: ['brand', 'badge'] },
      brand: {
        type: 'Brand',
        props: {
          eyebrow: '',
          title: 'birdy',
          subtitle: 'Prompt in, structured output.',
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
      workspace: { type: 'Workspace', props: {}, children: [] },
    };

    if (!authed) {
      elements.workspace.children = ['invite-panel'];
      elements['invite-panel'] = {
        type: 'Panel',
        props: {
          title: 'Unlock',
          subtitle: 'Authenticate once.',
        },
        children: ['invite-form'],
      };
      elements['invite-form'] = {
        type: 'InviteForm',
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

    elements.workspace.children = ['prompt-panel', 'result-panel'];
    elements['prompt-panel'] = {
      type: 'Panel',
      props: {
        title: 'Prompt',
        subtitle: 'Describe the task.',
      },
      children: ['prompt-form'],
    };
    elements['prompt-form'] = {
      type: 'PromptForm',
      props: {
        prompt,
        busy: genLoading,
        canSubmit: !genLoading && prompt.trim().length > 0,
        canClear: !genLoading && (genError.length > 0 || genText.length > 0 || genTools.length > 0),
        onChange: setPrompt,
        onSubmit: () => {
          void runGenerative();
        },
        onClear: clearOutput,
      },
      children: [],
    };

    const resultChildren: string[] = [];
    elements['result-panel'] = {
      type: 'Panel',
      props: {
        title: 'Result',
        subtitle: 'Rendered via json-render.',
      },
      children: resultChildren,
    };

    if (genError) {
      const k = 'result-status-error';
      resultChildren.push(k);
      elements[k] = { type: 'ResultStatus', props: { text: genError, tone: 'error' }, children: [] };
    } else if (genLoading) {
      const k = 'result-status-loading';
      resultChildren.push(k);
      elements[k] = {
        type: 'ResultStatus',
        props: { text: 'Generating...', tone: 'info' },
        children: [],
      };
    }

    if (genTools.length > 0) {
      const rowKey = 'tool-row';
      const toolChildren = genTools.map((_, i) => `tool-${i}`);
      resultChildren.push(rowKey);
      elements[rowKey] = { type: 'ToolsRow', props: {}, children: toolChildren };
      genTools.forEach((command, i) => {
        elements[`tool-${i}`] = { type: 'ToolChip', props: { command }, children: [] };
      });
    }

    const text = genText.trim();
    if (text) {
      const blocks = parseResultBlocks(text);
      blocks.forEach((block, i) => {
        if (block.kind === 'paragraph') {
          const key = `result-text-${i}`;
          resultChildren.push(key);
          elements[key] = { type: 'ResultText', props: { text: block.text }, children: [] };
          return;
        }
        if (block.kind === 'heading') {
          const key = `result-heading-${i}`;
          resultChildren.push(key);
          elements[key] = {
            type: 'ResultHeading',
            props: { text: block.text, level: block.level },
            children: [],
          };
          return;
        }
        const listKey = `result-list-${i}`;
        const listChildren = block.items.map((_, itemIndex) => `${listKey}-item-${itemIndex}`);
        resultChildren.push(listKey);
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
    } else if (!genLoading && !genError) {
      const key = 'result-empty';
      resultChildren.push(key);
      elements[key] = {
        type: 'EmptyHint',
        props: { text: 'No output yet.' },
        children: [],
      };
    }

    return { root: 'shell', elements };
  }, [authBusy, authStatus, authed, clearOutput, genError, genLoading, genText, genTools, inviteCode, prompt, runGenerative, verifyInviteCode]);

  return (
    <JSONUIProvider registry={registry}>
      <Renderer spec={uiSpec} registry={registry} />
    </JSONUIProvider>
  );
}
