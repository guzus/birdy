import { useEffect, useRef, useState, useCallback } from 'react';
import { Terminal } from 'xterm';
import { FitAddon } from 'xterm-addon-fit';
import { Renderer, DataProvider, VisibilityProvider } from '@json-render/react';
import { registry } from './registry';
import type { UITree } from '@json-render/core';
import type { CSSProperties } from 'react';

type AuthMsg = { type: 'auth'; ok: boolean; error?: string };
type UISpecMsg = { type: 'ui-spec'; spec: UITree; streaming?: boolean };
type UISpecClearMsg = { type: 'ui-spec-clear' };

type ControlMsg = AuthMsg | UISpecMsg | UISpecClearMsg;

const inviteCodeKey = 'birdy_host_invite_code';
const inputFlushDelayMs = 2;
const inputChunkMaxChars = 1536;
const immediateInputChars = 8;
const touchScrollStepPx = 24;
const touchScrollMaxStepsPerMove = 10;

export default function App() {
  const termRef = useRef<HTMLDivElement>(null);
  const terminalRef = useRef<Terminal | null>(null);
  const fitAddonRef = useRef<FitAddon | null>(null);
  const wsRef = useRef<WebSocket | null>(null);
  const authedRef = useRef(false);
  const inviteCodeRef = useRef('');
  const inputEncoderRef = useRef(new TextEncoder());
  const utf8DecoderRef = useRef(new TextDecoder());

  const pendingInputRef = useRef('');
  const inputFlushTimerRef = useRef(0);
  const lastColsRef = useRef(0);
  const lastRowsRef = useRef(0);
  const touchLastYRef = useRef<number | null>(null);
  const touchAccumRef = useRef(0);

  const [status, setStatus] = useState('invite code required');
  const [uiSpec, setUISpec] = useState<UITree | null>(null);
  const [panelOpen, setPanelOpen] = useState(false);
  const [isStreaming, setIsStreaming] = useState(false);

  const sendResize = useCallback(() => {
    const term = terminalRef.current;
    const ws = wsRef.current;
    if (!authedRef.current || !ws || ws.readyState !== WebSocket.OPEN || !term) return;
    if (term.cols === lastColsRef.current && term.rows === lastRowsRef.current) return;
    lastColsRef.current = term.cols;
    lastRowsRef.current = term.rows;
    ws.send(JSON.stringify({ type: 'resize', cols: term.cols, rows: term.rows }));
  }, []);

  const fitAndResize = useCallback(() => {
    fitAddonRef.current?.fit();
    sendResize();
  }, [sendResize]);

  const sendInputChunk = useCallback((chunk: string) => {
    if (!chunk) return;
    const ws = wsRef.current;
    if (!authedRef.current || !ws || ws.readyState !== WebSocket.OPEN) return;
    for (let i = 0; i < chunk.length; i += inputChunkMaxChars) {
      const part = chunk.slice(i, i + inputChunkMaxChars);
      if (part) ws.send(inputEncoderRef.current.encode(part));
    }
  }, []);

  const flushInput = useCallback(() => {
    if (!pendingInputRef.current) return;
    if (!authedRef.current || !wsRef.current || wsRef.current.readyState !== WebSocket.OPEN) return;
    const chunk = pendingInputRef.current;
    pendingInputRef.current = '';
    sendInputChunk(chunk);
  }, [sendInputChunk]);

  const scheduleInputFlush = useCallback(() => {
    if (inputFlushTimerRef.current !== 0) return;
    inputFlushTimerRef.current = window.setTimeout(() => {
      inputFlushTimerRef.current = 0;
      flushInput();
    }, inputFlushDelayMs);
  }, [flushInput]);

  const isUrgentInput = useCallback((data: string) => {
    if (!data) return false;
    return data.includes('\r') || data.includes('\n') || data.includes('\x7f') || data.includes('\x1b') || data.length >= 64;
  }, []);

  const enqueueInputData = useCallback((data: string) => {
    if (!data) return;
    if (!authedRef.current || !wsRef.current || wsRef.current.readyState !== WebSocket.OPEN) return;

    if (pendingInputRef.current.length === 0 && data.length > 0 && data.length <= immediateInputChars) {
      sendInputChunk(data);
      return;
    }

    pendingInputRef.current += data;
    if (pendingInputRef.current.length >= inputChunkMaxChars || isUrgentInput(data)) {
      if (inputFlushTimerRef.current !== 0) {
        window.clearTimeout(inputFlushTimerRef.current);
        inputFlushTimerRef.current = 0;
      }
      flushInput();
      return;
    }

    scheduleInputFlush();
  }, [sendInputChunk, flushInput, scheduleInputFlush, isUrgentInput]);

  const handleControlMessage = useCallback((msg: ControlMsg) => {
    if (msg.type === 'ui-spec') {
      setUISpec(msg.spec);
      setIsStreaming(msg.streaming ?? false);
      setPanelOpen(true);
      return true;
    }
    if (msg.type === 'ui-spec-clear') {
      setUISpec(null);
      setIsStreaming(false);
      return true;
    }
    return false;
  }, []);

  const connect = useCallback(() => {
    const proto = window.location.protocol === 'https:' ? 'wss' : 'ws';
    const url = `${proto}://${window.location.host}/ws`;
    const ws = new WebSocket(url);
    ws.binaryType = 'arraybuffer';
    wsRef.current = ws;
    authedRef.current = false;
    setStatus('authenticating...');

    ws.onopen = () => {
      ws.send(JSON.stringify({ type: 'auth', code: inviteCodeRef.current }));
    };

    ws.onclose = () => {
      const tail = utf8DecoderRef.current.decode();
      if (tail) terminalRef.current?.write(tail);

      if (inputFlushTimerRef.current !== 0) {
        window.clearTimeout(inputFlushTimerRef.current);
        inputFlushTimerRef.current = 0;
      }
      pendingInputRef.current = '';

      if (!authedRef.current) {
        window.sessionStorage.removeItem(inviteCodeKey);
        setStatus('invalid invite code');
        window.setTimeout(startAuthFlow, 150);
        return;
      }
      setStatus('disconnected');
    };

    ws.onerror = () => {
      setStatus(authedRef.current ? 'error' : 'auth failed');
    };

    ws.onmessage = (event) => {
      if (typeof event.data === 'string') {
        let control: unknown = null;
        try { control = JSON.parse(event.data); } catch { control = null; }

        const auth = control as Partial<AuthMsg> | null;
        if (auth && auth.type === 'auth') {
          if (!auth.ok) {
            setStatus(auth.error || 'invalid invite code');
            return;
          }
          authedRef.current = true;
          window.sessionStorage.setItem(inviteCodeKey, inviteCodeRef.current);
          setStatus('live');
          fitAndResize();
          requestAnimationFrame(fitAndResize);
          setTimeout(fitAndResize, 100);
          setTimeout(fitAndResize, 350);
          terminalRef.current?.focus();
          return;
        }

        // Try handling as a json-render control message.
        if (control && typeof control === 'object' && 'type' in control) {
          if (handleControlMessage(control as ControlMsg)) return;
        }

        if (authedRef.current) {
          terminalRef.current?.write(event.data);
        }
        return;
      }

      if (!authedRef.current) return;

      if (event.data instanceof ArrayBuffer) {
        const text = utf8DecoderRef.current.decode(event.data, { stream: true });
        if (text) terminalRef.current?.write(text);
        return;
      }

      const anyData = event.data as { arrayBuffer?: () => Promise<ArrayBuffer> };
      if (anyData && typeof anyData.arrayBuffer === 'function') {
        anyData.arrayBuffer().then((buf) => {
          const text = utf8DecoderRef.current.decode(buf, { stream: true });
          if (text) terminalRef.current?.write(text);
        });
      }
    };
  }, [fitAndResize, handleControlMessage]);

  const startAuthFlow = useCallback(() => {
    const prior = window.sessionStorage.getItem(inviteCodeKey) || '';
    const entered = window.prompt('Enter invite code', prior);
    if (entered === null) {
      setStatus('invite code required');
      return;
    }
    inviteCodeRef.current = entered.trim();
    if (!inviteCodeRef.current) {
      setStatus('invite code required');
      return;
    }
    connect();
  }, [connect]);

  // Initialize terminal.
  useEffect(() => {
    if (!termRef.current) return;

    const term = new Terminal({
      cursorBlink: true,
      scrollback: 5000,
      theme: { background: '#000000', foreground: '#d8f2ff', cursor: '#38bdf8' },
    });
    const fitAddon = new FitAddon();
    term.loadAddon(fitAddon);
    term.open(termRef.current);
    fitAddon.fit();

    terminalRef.current = term;
    fitAddonRef.current = fitAddon;

    term.onData((data) => enqueueInputData(data));

    term.attachCustomKeyEventHandler((event) => {
      if (!authedRef.current || !wsRef.current || wsRef.current.readyState !== WebSocket.OPEN) return true;
      if (event.type !== 'keydown') return true;
      if (event.ctrlKey && !event.altKey && !event.metaKey && event.key.toLowerCase() === 'y') {
        event.preventDefault();
        enqueueInputData('\x19');
        return false;
      }
      return true;
    });

    return () => {
      term.dispose();
    };
  }, [enqueueInputData]);

  // Touch scroll handlers.
  useEffect(() => {
    const el = termRef.current;
    if (!el) return;

    const resetTouchScroll = () => { touchLastYRef.current = null; touchAccumRef.current = 0; };

    const onTouchStart = (event: TouchEvent) => {
      if (event.touches.length !== 1) { resetTouchScroll(); return; }
      touchLastYRef.current = event.touches[0].clientY;
      touchAccumRef.current = 0;
    };

    const onTouchMove = (event: TouchEvent) => {
      if (!authedRef.current || !wsRef.current || wsRef.current.readyState !== WebSocket.OPEN) return;
      if (event.touches.length !== 1 || touchLastYRef.current === null) { resetTouchScroll(); return; }
      const y = event.touches[0].clientY;
      const delta = y - touchLastYRef.current;
      touchLastYRef.current = y;
      touchAccumRef.current += delta;
      let steps = Math.floor(Math.abs(touchAccumRef.current) / touchScrollStepPx);
      if (steps <= 0) return;
      event.preventDefault();
      if (steps > touchScrollMaxStepsPerMove) steps = touchScrollMaxStepsPerMove;
      const scrollUp = touchAccumRef.current > 0;
      const used = steps * touchScrollStepPx;
      touchAccumRef.current += scrollUp ? -used : used;
      const seq = scrollUp ? '\x1b[A' : '\x1b[B';
      enqueueInputData(seq.repeat(steps));
    };

    el.addEventListener('touchstart', onTouchStart, { passive: true });
    el.addEventListener('touchmove', onTouchMove, { passive: false });
    el.addEventListener('touchend', resetTouchScroll, { passive: true });
    el.addEventListener('touchcancel', resetTouchScroll, { passive: true });

    return () => {
      el.removeEventListener('touchstart', onTouchStart);
      el.removeEventListener('touchmove', onTouchMove);
      el.removeEventListener('touchend', resetTouchScroll);
      el.removeEventListener('touchcancel', resetTouchScroll);
    };
  }, [enqueueInputData]);

  // Resize handling.
  useEffect(() => {
    let rafId = 0;
    const scheduleFit = () => {
      if (rafId !== 0) return;
      rafId = requestAnimationFrame(() => { rafId = 0; fitAndResize(); });
    };

    window.addEventListener('resize', scheduleFit);

    let observer: ResizeObserver | undefined;
    if (termRef.current && typeof ResizeObserver !== 'undefined') {
      observer = new ResizeObserver(scheduleFit);
      observer.observe(termRef.current);
    }

    return () => {
      window.removeEventListener('resize', scheduleFit);
      observer?.disconnect();
      if (rafId) cancelAnimationFrame(rafId);
    };
  }, [fitAndResize]);

  // Start auth on load.
  useEffect(() => {
    fitAndResize();
    startAuthFlow();
  }, [fitAndResize, startAuthFlow]);

  // Re-fit terminal when panel toggles.
  useEffect(() => {
    const id = requestAnimationFrame(fitAndResize);
    return () => cancelAnimationFrame(id);
  }, [panelOpen, fitAndResize]);

  const topBarStyle: CSSProperties = {
    boxSizing: 'border-box',
    display: 'flex',
    justifyContent: 'space-between',
    alignItems: 'center',
    padding: '8px 12px',
    borderBottom: '1px solid var(--border)',
    background: 'var(--top-bg)',
    color: 'var(--accent)',
    fontWeight: 700,
    letterSpacing: '0.03em',
  };

  const panelToggleStyle: CSSProperties = {
    background: 'none',
    border: `1px solid ${uiSpec ? 'var(--accent)' : '#334155'}`,
    color: uiSpec ? 'var(--accent)' : 'var(--muted)',
    borderRadius: 4,
    padding: '2px 8px',
    fontSize: 11,
    fontWeight: 600,
    cursor: 'pointer',
    fontFamily: 'inherit',
    marginLeft: 8,
  };

  return (
    <div id="root-layout" style={{ display: 'grid', gridTemplateRows: 'auto minmax(0, 1fr)', height: '100%', minHeight: '100%' }}>
      <div style={topBarStyle}>
        <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
          <span>BIRDY HOST</span>
          {uiSpec && (
            <button style={panelToggleStyle} onClick={() => setPanelOpen((v) => !v)}>
              {panelOpen ? 'HIDE UI' : 'SHOW UI'}
            </button>
          )}
        </div>
        <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
          {isStreaming && (
            <span style={{ color: 'var(--accent)', fontSize: 11, animation: 'pulse 1.5s infinite' }}>
              STREAMING
            </span>
          )}
          <span id="status" style={{ color: 'var(--muted)', fontWeight: 500 }}>{status}</span>
        </div>
      </div>
      <div style={{ display: 'flex', minHeight: 0, overflow: 'hidden' }}>
        <div
          ref={termRef}
          style={{
            flex: panelOpen ? '1 1 60%' : '1 1 100%',
            minWidth: 0,
            height: '100%',
            boxSizing: 'border-box',
            overflow: 'hidden',
            touchAction: 'none',
          }}
        />
        {panelOpen && uiSpec && (
          <div style={{
            flex: '0 0 40%',
            maxWidth: '40%',
            height: '100%',
            overflow: 'auto',
            borderLeft: '1px solid var(--border)',
            background: '#010a12',
            padding: 16,
            boxSizing: 'border-box',
          }}>
            <DataProvider>
              <VisibilityProvider>
                <Renderer tree={uiSpec} registry={registry} loading={isStreaming} />
              </VisibilityProvider>
            </DataProvider>
          </div>
        )}
      </div>
    </div>
  );
}
