import { expect, test } from 'bun:test';
import { renderToStaticMarkup } from 'react-dom/server';
import { MarkdownMessage } from './App';

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
