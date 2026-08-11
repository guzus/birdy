import { expect, test } from 'bun:test';
import { renderToStaticMarkup } from 'react-dom/server';
import { Composer, MarkdownMessage, SharedSnapshotView } from './App';

test('renders a responsive semantic table with inline formatting', () => {
  const markup = renderToStaticMarkup(
    <MarkdownMessage
      text={`| 활동 | 멀티플라이어 |
| --- | ---: |
| Pendle에서 YT-cUSD 또는 LP-cUSD 보유 | **5x** (최고 효율) |`}
    />,
  );

  expect(markup).toContain('overflow-x-auto');
  expect(markup).toContain('<table');
  expect(markup).toContain('<thead');
  expect(markup).toContain('<tbody');
  expect(markup).toContain('<strong');
  expect(markup).toContain('>5x</strong>');
  expect(markup).toContain('text-align:right');
});

test('renders a responsive model picker with unavailable choices and status', () => {
  const markup = renderToStaticMarkup(
    <Composer
      prompt="hello"
      busy={false}
      models={[
        { id: 'sonnet', provider: 'Claude Code', model: 'Sonnet', supportsBirdyTools: true, available: true },
        {
          id: 'deepseek-flash',
          provider: 'OpenCode Go',
          model: 'DeepSeek V4 Flash',
          supportsBirdyTools: true,
          available: false,
          unavailableReason: 'Host not configured',
        },
      ]}
      modelId="sonnet"
      modelStatus=""
      onChange={() => {}}
      onModelChange={() => {}}
      onSend={() => {}}
    />,
  );

  expect(markup).toContain('aria-label="Chat model"');
  expect(markup).toContain('OpenCode Go · DeepSeek V4 Flash — unavailable');
  expect(markup).toContain('value="deepseek-flash" disabled=""');
  expect(markup).toContain('flex-wrap');
  expect(markup).toContain('max-w-[250px]');
  expect(markup).toContain('sm:max-w-[360px]');
  expect(markup).toContain('aria-live="polite"');
  expect(markup).toContain('Birdy tools enabled');
});

test('renders a read-only shared snapshot without authenticated controls', () => {
  const markup = renderToStaticMarkup(
    <SharedSnapshotView
      snapshot={{
        version: 1,
        title: 'Signals worth sharing',
        conversationCreatedAt: '2026-08-01T00:00:00.000Z',
        cards: [{
          category: 'AI', title: 'A useful signal', bullets: ['First point'], sources: ['@source'],
          timestamp: '2026-08-01T01:00:00.000Z',
        }],
        messages: [
          { role: 'user', text: 'What happened?' },
          { role: 'assistant', text: '**Answer** with context.', modelLabel: 'Sonnet' },
        ],
      }}
      expiresAt="2026-08-08T00:00:00.000Z"
    />,
  );

  expect(markup).toContain('Read-only snapshot');
  expect(markup).toContain('Signals worth sharing');
  expect(markup).toContain('A useful signal');
  expect(markup).toContain('<strong class="font-semibold text-text">Answer</strong>');
  expect(markup.includes('Ask anything')).toBe(false);
  expect(markup.includes('Deep dive')).toBe(false);
  expect(markup.includes('Scan Timeline')).toBe(false);
  expect(markup.includes('Enter invite code')).toBe(false);
});

test('turns bare X URLs into external links', () => {
  const markup = renderToStaticMarkup(
    <MarkdownMessage text="Read x.com/birdy/status/123, then https://x.com/birdy/status/456." />,
  );

  expect(markup).toContain('href="https://x.com/birdy/status/123"');
  expect(markup).toContain('>x.com/birdy/status/123</a>,');
  expect(markup).toContain('href="https://x.com/birdy/status/456"');
  expect(markup).toContain('>https://x.com/birdy/status/456</a>.');
  expect(markup.match(/target="_blank"/g)).toHaveLength(2);
});

test('does not link X-like text inside code or another URL', () => {
  const markup = renderToStaticMarkup(
    <MarkdownMessage text={'Run `birdy read x.com/birdy/status/123` or visit example.com/x.com/not-a-link.'} />,
  );

  expect(markup).not.toContain('<a');
  expect(markup).toContain('<code');
});
