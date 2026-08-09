import { expect, test } from 'bun:test';
import { renderToStaticMarkup } from 'react-dom/server';
import { Composer, MarkdownMessage } from './App';

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
