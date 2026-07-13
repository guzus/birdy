export type MarkdownTableAlignment = 'left' | 'center' | 'right' | null;

export type MarkdownBlock =
  | { kind: 'heading'; level: 1 | 2 | 3 | 4; text: string }
  | { kind: 'paragraph'; lines: string[] }
  | { kind: 'ul'; items: string[] }
  | { kind: 'ol'; items: string[] }
  | { kind: 'code'; code: string }
  | { kind: 'table'; headers: string[]; alignments: MarkdownTableAlignment[]; rows: string[][] }
  | { kind: 'rule' };

function isMarkdownRule(line: string): boolean {
  return /^ {0,3}([-*_])(?:\s*\1){2,}\s*$/.test(line);
}

function splitTableRow(line: string): string[] | null {
  const trimmed = line.trim();
  if (!trimmed.includes('|')) return null;

  const cells: string[] = [];
  let cell = '';
  let escaped = false;
  let codeFenceLength = 0;

  for (let i = 0; i < trimmed.length; i++) {
    const char = trimmed[i];

    if (escaped) {
      cell += char === '|' ? '|' : `\\${char}`;
      escaped = false;
      continue;
    }

    if (char === '\\') {
      escaped = true;
      continue;
    }

    if (char === '`') {
      let runLength = 1;
      while (trimmed[i + runLength] === '`') runLength++;
      if (codeFenceLength === 0) codeFenceLength = runLength;
      else if (codeFenceLength === runLength) codeFenceLength = 0;
      cell += '`'.repeat(runLength);
      i += runLength - 1;
      continue;
    }

    if (char === '|' && codeFenceLength === 0) {
      cells.push(cell.trim());
      cell = '';
      continue;
    }

    cell += char;
  }

  if (escaped) cell += '\\';
  cells.push(cell.trim());

  if (trimmed.startsWith('|')) cells.shift();
  if (trimmed.endsWith('|') && !trimmed.endsWith('\\|')) cells.pop();
  return cells.length >= 2 ? cells : null;
}

function parseDelimiterRow(line: string): MarkdownTableAlignment[] | null {
  const cells = splitTableRow(line);
  if (!cells || cells.some((cell) => !/^:?-{3,}:?$/.test(cell))) return null;

  return cells.map((cell) => {
    const left = cell.startsWith(':');
    const right = cell.endsWith(':');
    if (left && right) return 'center';
    if (right) return 'right';
    if (left) return 'left';
    return null;
  });
}

function parseTableStart(lines: string[], index: number) {
  if (index + 1 >= lines.length) return null;
  const headers = splitTableRow(lines[index]);
  const alignments = parseDelimiterRow(lines[index + 1]);
  if (!headers || !alignments || headers.length !== alignments.length) return null;
  return { headers, alignments };
}

function isMarkdownBoundary(lines: string[], index: number): boolean {
  const trimmed = lines[index].trim();
  if (!trimmed) return true;
  if (parseTableStart(lines, index)) return true;
  if (/^#{1,4}\s+/.test(trimmed)) return true;
  if (/^```/.test(trimmed)) return true;
  if (/^[-*•]\s+/.test(trimmed)) return true;
  if (/^\d+\.\s+/.test(trimmed)) return true;
  return isMarkdownRule(trimmed);
}

export function parseMarkdownBlocks(markdown: string): MarkdownBlock[] {
  const normalized = markdown.replace(/\r\n?/g, '\n').trim();
  if (!normalized) return [];

  const lines = normalized.split('\n');
  const blocks: MarkdownBlock[] = [];

  for (let i = 0; i < lines.length; ) {
    const line = lines[i];
    const trimmed = line.trim();

    if (!trimmed) {
      i++;
      continue;
    }

    if (/^```/.test(trimmed)) {
      const codeLines: string[] = [];
      i++;
      while (i < lines.length && !/^```/.test(lines[i].trim())) {
        codeLines.push(lines[i]);
        i++;
      }
      if (i < lines.length) i++;
      blocks.push({ kind: 'code', code: codeLines.join('\n') });
      continue;
    }

    const headingMatch = trimmed.match(/^(#{1,4})\s+(.+)$/);
    if (headingMatch) {
      blocks.push({
        kind: 'heading',
        level: headingMatch[1].length as 1 | 2 | 3 | 4,
        text: headingMatch[2].trim(),
      });
      i++;
      continue;
    }

    if (isMarkdownRule(trimmed)) {
      blocks.push({ kind: 'rule' });
      i++;
      continue;
    }

    const table = parseTableStart(lines, i);
    if (table) {
      const rows: string[][] = [];
      i += 2;
      while (i < lines.length && lines[i].trim()) {
        if (isMarkdownBoundary(lines, i)) break;
        const cells = splitTableRow(lines[i]);
        if (!cells) break;
        rows.push(table.headers.map((_, column) => cells[column] ?? ''));
        i++;
      }
      blocks.push({ kind: 'table', ...table, rows });
      continue;
    }

    if (/^[-*•]\s+/.test(trimmed)) {
      const items: string[] = [];
      while (i < lines.length) {
        const match = lines[i].trim().match(/^[-*•]\s+(.+)$/);
        if (!match) break;
        items.push(match[1].trim());
        i++;
      }
      blocks.push({ kind: 'ul', items });
      continue;
    }

    if (/^\d+\.\s+/.test(trimmed)) {
      const items: string[] = [];
      while (i < lines.length) {
        const match = lines[i].trim().match(/^\d+\.\s+(.+)$/);
        if (!match) break;
        items.push(match[1].trim());
        i++;
      }
      blocks.push({ kind: 'ol', items });
      continue;
    }

    const paragraphLines: string[] = [];
    while (i < lines.length) {
      if (!lines[i].trim()) break;
      if (paragraphLines.length > 0 && isMarkdownBoundary(lines, i)) break;
      paragraphLines.push(lines[i].trim());
      i++;
    }
    blocks.push({ kind: 'paragraph', lines: paragraphLines });
  }

  return blocks;
}
