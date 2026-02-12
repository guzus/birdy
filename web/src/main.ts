import './style.css';
import 'xterm/css/xterm.css';

import { Terminal } from 'xterm';
import { FitAddon } from 'xterm-addon-fit';

type AuthMsg = { type: 'auth'; ok: boolean; error?: string };

const statusEl = document.getElementById('status');
const termEl = document.getElementById('term');

if (!statusEl || !termEl) {
  throw new Error('missing host DOM');
}

const inviteCodeKey = 'birdy_host_invite_code';

const term = new Terminal({
  cursorBlink: true,
  scrollback: 5000,
  theme: {
    background: '#000000',
    foreground: '#d8f2ff',
    cursor: '#38bdf8',
  },
});

const fitAddon = new FitAddon();
term.loadAddon(fitAddon);
term.open(termEl);
fitAddon.fit();

let ws: WebSocket | null = null;
let inviteCode = '';
let authed = false;

const utf8Decoder = new TextDecoder();
const inputEncoder = new TextEncoder();

const inputFlushDelayMs = 2;
const inputChunkMaxChars = 1536;
const immediateInputChars = 8;

const touchScrollStepPx = 24;
const touchScrollMaxStepsPerMove = 10;

let pendingInput = '';
let inputFlushTimer = 0;

let lastCols = 0;
let lastRows = 0;

let touchLastY: number | null = null;
let touchAccum = 0;

function requestWSProto() {
  return window.location.protocol === 'https:' ? 'wss' : 'ws';
}

function sendResize() {
  if (!authed || !ws || ws.readyState !== WebSocket.OPEN) return;
  if (term.cols === lastCols && term.rows === lastRows) return;
  lastCols = term.cols;
  lastRows = term.rows;
  ws.send(JSON.stringify({ type: 'resize', cols: term.cols, rows: term.rows }));
}

function fitAndResize() {
  fitAddon.fit();
  sendResize();
}

function sendInputChunk(chunk: string) {
  if (!chunk) return;
  if (!authed || !ws || ws.readyState !== WebSocket.OPEN) return;

  for (let i = 0; i < chunk.length; i += inputChunkMaxChars) {
    const part = chunk.slice(i, i + inputChunkMaxChars);
    if (!part) continue;
    ws.send(inputEncoder.encode(part));
  }
}

function flushInput() {
  if (!pendingInput) return;
  if (!authed || !ws || ws.readyState !== WebSocket.OPEN) return;

  const chunk = pendingInput;
  pendingInput = '';
  sendInputChunk(chunk);
}

function scheduleInputFlush() {
  if (inputFlushTimer !== 0) return;
  inputFlushTimer = window.setTimeout(() => {
    inputFlushTimer = 0;
    flushInput();
  }, inputFlushDelayMs);
}

function isUrgentInput(data: string) {
  if (!data) return false;
  return (
    data.includes('\r') ||
    data.includes('\n') ||
    data.includes('\x7f') ||
    data.includes('\x1b') ||
    data.length >= 64
  );
}

function enqueueInputData(data: string) {
  if (!data) return;
  if (!authed || !ws || ws.readyState !== WebSocket.OPEN) return;

  if (pendingInput.length === 0 && data.length > 0 && data.length <= immediateInputChars) {
    sendInputChunk(data);
    return;
  }

  pendingInput += data;
  if (pendingInput.length >= inputChunkMaxChars || isUrgentInput(data)) {
    if (inputFlushTimer !== 0) {
      window.clearTimeout(inputFlushTimer);
      inputFlushTimer = 0;
    }
    flushInput();
    return;
  }

  scheduleInputFlush();
}

function startAuthFlow() {
  const prior = window.sessionStorage.getItem(inviteCodeKey) || '';
  const entered = window.prompt('Enter invite code', prior);
  if (entered === null) {
    statusEl.textContent = 'invite code required';
    return;
  }
  inviteCode = entered.trim();
  if (!inviteCode) {
    statusEl.textContent = 'invite code required';
    return;
  }
  connect();
}

function connect() {
  const url = `${requestWSProto()}://${window.location.host}/ws`;
  ws = new WebSocket(url);
  ws.binaryType = 'arraybuffer';
  authed = false;
  statusEl.textContent = 'authenticating...';

  ws.onopen = () => {
    ws?.send(JSON.stringify({ type: 'auth', code: inviteCode }));
  };

  ws.onclose = () => {
    const tail = utf8Decoder.decode();
    if (tail) term.write(tail);

    if (inputFlushTimer !== 0) {
      window.clearTimeout(inputFlushTimer);
      inputFlushTimer = 0;
    }
    pendingInput = '';

    if (!authed) {
      window.sessionStorage.removeItem(inviteCodeKey);
      statusEl.textContent = 'invalid invite code';
      window.setTimeout(startAuthFlow, 150);
      return;
    }
    statusEl.textContent = 'disconnected';
  };

  ws.onerror = () => {
    statusEl.textContent = authed ? 'error' : 'auth failed';
  };

  ws.onmessage = (event) => {
    if (typeof event.data === 'string') {
      let control: unknown = null;
      try {
        control = JSON.parse(event.data);
      } catch {
        control = null;
      }

      const auth = control as Partial<AuthMsg> | null;
      if (auth && auth.type === 'auth') {
        if (!auth.ok) {
          statusEl.textContent = auth.error || 'invalid invite code';
          return;
        }
        authed = true;
        window.sessionStorage.setItem(inviteCodeKey, inviteCode);
        statusEl.textContent = 'live';
        fitAndResize();
        requestAnimationFrame(fitAndResize);
        setTimeout(fitAndResize, 100);
        setTimeout(fitAndResize, 350);
        term.focus();
        return;
      }

      if (authed) {
        term.write(event.data);
      }
      return;
    }

    if (!authed) return;

    if (event.data instanceof ArrayBuffer) {
      const text = utf8Decoder.decode(event.data, { stream: true });
      if (text) term.write(text);
      return;
    }

    // Blob fallback
    const anyData = event.data as { arrayBuffer?: () => Promise<ArrayBuffer> };
    if (anyData && typeof anyData.arrayBuffer === 'function') {
      anyData.arrayBuffer().then((buf) => {
        const text = utf8Decoder.decode(buf, { stream: true });
        if (text) term.write(text);
      });
    }
  };
}

term.onData((data) => {
  enqueueInputData(data);
});

term.attachCustomKeyEventHandler((event) => {
  if (!authed || !ws || ws.readyState !== WebSocket.OPEN) {
    return true;
  }
  if (event.type !== 'keydown') {
    return true;
  }

  if (event.ctrlKey && !event.altKey && !event.metaKey && event.key.toLowerCase() === 'y') {
    event.preventDefault();
    enqueueInputData('\x19');
    return false;
  }

  return true;
});

function resetTouchScroll() {
  touchLastY = null;
  touchAccum = 0;
}

function handleTouchMove(event: TouchEvent) {
  if (!authed || !ws || ws.readyState !== WebSocket.OPEN) {
    return;
  }
  if (event.touches.length !== 1 || touchLastY === null) {
    resetTouchScroll();
    return;
  }

  const y = event.touches[0].clientY;
  const delta = y - touchLastY;
  touchLastY = y;
  touchAccum += delta;

  let steps = Math.floor(Math.abs(touchAccum) / touchScrollStepPx);
  if (steps <= 0) return;

  event.preventDefault();
  if (steps > touchScrollMaxStepsPerMove) steps = touchScrollMaxStepsPerMove;

  const scrollUp = touchAccum > 0;
  const used = steps * touchScrollStepPx;
  touchAccum += scrollUp ? -used : used;

  const seq = scrollUp ? '\x1b[A' : '\x1b[B';
  enqueueInputData(seq.repeat(steps));
}

termEl.addEventListener(
  'touchstart',
  (event: TouchEvent) => {
    if (event.touches.length !== 1) {
      resetTouchScroll();
      return;
    }
    touchLastY = event.touches[0].clientY;
    touchAccum = 0;
  },
  { passive: true },
);

termEl.addEventListener('touchmove', handleTouchMove, { passive: false });
termEl.addEventListener('touchend', resetTouchScroll, { passive: true });
termEl.addEventListener('touchcancel', resetTouchScroll, { passive: true });

let resizeRAF = 0;
function scheduleFitAndResize() {
  if (resizeRAF !== 0) return;
  resizeRAF = requestAnimationFrame(() => {
    resizeRAF = 0;
    fitAndResize();
  });
}

window.addEventListener('resize', scheduleFitAndResize);
window.addEventListener('load', () => {
  scheduleFitAndResize();
  startAuthFlow();
});

if (typeof ResizeObserver !== 'undefined') {
  const observer = new ResizeObserver(scheduleFitAndResize);
  observer.observe(termEl);
}
