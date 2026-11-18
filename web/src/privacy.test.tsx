import { expect, test } from 'bun:test';
import { renderToStaticMarkup } from 'react-dom/server';
import { RelatedLinks } from './App';
import { PrivacyPage, privacyPath } from './privacy';

test('the privacy notice states what the usage counts keep', () => {
  const markup = renderToStaticMarkup(<PrivacyPage />);
  for (const text of [
    'Usage counts',
    "Usage counts are off unless the site's operator turns them on. When they are on",
    'chat, scan briefings and commands',
    'the engine that answered',
    'salted hash (HMAC-SHA256) of the IP address',
    'The key for these hashes changes on the first of every month (it is derived from a secret)',
    'within a calendar month the same address gets the same hash every day',
    'Hashes are deleted after 35 days',
    'No IP addresses are kept',
    'no chat, scan or command content is stored',
    'Nothing is shared or sold',
    'no third-party analytics',
  ]) {
    expect(markup.replace(/\s+/g, ' ')).toContain(text);
  }
  // Nothing loads or links off the site.
  expect(/(src|href)="https?:/.test(markup)).toBe(false);
});

test('every page links to the privacy notice', () => {
  const markup = renderToStaticMarkup(<RelatedLinks />);
  expect(privacyPath).toBe('/privacy');
  expect(markup).toContain('href="/privacy"');
  expect(markup).toContain('>Privacy</a>');
});
