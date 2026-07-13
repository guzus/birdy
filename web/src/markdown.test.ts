import { describe, expect, test } from 'bun:test';
import { parseMarkdownBlocks } from './markdown';

describe('parseMarkdownBlocks tables', () => {
  test('parses the Korean response table and preserves inline markdown', () => {
    const markdown = `2026년 6월 1일 이후:

| 활동 | 멀티플라이어 |
|---|---|
| Pendle에서 YT-cUSD 또는 LP-cUSD 보유 | **5x** (최고 효율) |
| cUSD 단순 보유 | 2.5x |`;

    expect(parseMarkdownBlocks(markdown)).toEqual([
      { kind: 'paragraph', lines: ['2026년 6월 1일 이후:'] },
      {
        kind: 'table',
        headers: ['활동', '멀티플라이어'],
        alignments: [null, null],
        rows: [
          ['Pendle에서 YT-cUSD 또는 LP-cUSD 보유', '**5x** (최고 효율)'],
          ['cUSD 단순 보유', '2.5x'],
        ],
      },
    ]);
  });

  test('supports outer-pipe-free rows, alignment, escaped pipes, and missing cells', () => {
    const markdown = `Name | Detail | Score
:--- | :---: | ---:
alpha | \`a|b\` and x\\|y | 10
beta | only detail`;

    expect(parseMarkdownBlocks(markdown)).toEqual([
      {
        kind: 'table',
        headers: ['Name', 'Detail', 'Score'],
        alignments: ['left', 'center', 'right'],
        rows: [
          ['alpha', '`a|b` and x|y', '10'],
          ['beta', 'only detail', ''],
        ],
      },
    ]);
  });

  test('leaves malformed delimiters with fewer than three hyphens as text', () => {
    const blocks = parseMarkdownBlocks(`| A | B |\n| -- | --- |\n| one | two |`);

    expect(blocks).toEqual([
      { kind: 'paragraph', lines: ['| A | B |', '| -- | --- |', '| one | two |'] },
    ]);
  });

  test('stops a table before the next Markdown block', () => {
    const blocks = parseMarkdownBlocks(`| A | B |\n| --- | --- |\n| one | two |\n## Next | section`);

    expect(blocks).toEqual([
      {
        kind: 'table',
        headers: ['A', 'B'],
        alignments: [null, null],
        rows: [['one', 'two']],
      },
      { kind: 'heading', level: 2, text: 'Next | section' },
    ]);
  });
});
