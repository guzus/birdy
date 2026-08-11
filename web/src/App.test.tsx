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
