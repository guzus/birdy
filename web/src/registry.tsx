import type { ComponentRegistry, ComponentRenderProps } from '@json-render/react';
import type { CSSProperties } from 'react';

const colors = {
  bg: '#000',
  fg: '#d8f2ff',
  muted: '#cbd5e1',
  accent: '#38bdf8',
  border: '#0ea5e9',
  cardBg: '#061520',
  success: '#22c55e',
  warning: '#eab308',
  error: '#ef4444',
};

const baseFontFamily = 'ui-monospace, SFMono-Regular, Menlo, monospace';

function CardComponent({ element, children }: ComponentRenderProps) {
  const { title, description, variant } = element.props as {
    title: string | null; description: string | null; variant: string | null;
  };
  const borderColor =
    variant === 'accent' ? colors.accent :
    variant === 'muted' ? '#334155' :
    colors.border;
  const style: CSSProperties = {
    border: `1px solid ${borderColor}`,
    borderRadius: 8,
    padding: '12px 16px',
    background: colors.cardBg,
    fontFamily: baseFontFamily,
  };
  return (
    <div style={style}>
      {title && (
        <div style={{ color: colors.accent, fontWeight: 700, fontSize: 14, marginBottom: description || children ? 6 : 0 }}>
          {title}
        </div>
      )}
      {description && (
        <div style={{ color: colors.muted, fontSize: 13, marginBottom: children ? 8 : 0 }}>
          {description}
        </div>
      )}
      {children}
    </div>
  );
}

function HeadingComponent({ element }: ComponentRenderProps) {
  const { text, level } = element.props as { text: string; level: string | null };
  const sizes: Record<string, number> = { '1': 20, '2': 16, '3': 14 };
  const sz = sizes[level ?? '2'] ?? 16;
  return (
    <div style={{ color: colors.fg, fontWeight: 700, fontSize: sz, fontFamily: baseFontFamily, marginBottom: 4 }}>
      {text}
    </div>
  );
}

function TextComponent({ element }: ComponentRenderProps) {
  const { content, variant, size } = element.props as {
    content: string; variant: string | null; size: string | null;
  };
  const colorMap: Record<string, string> = {
    default: colors.fg, muted: colors.muted, accent: colors.accent, error: colors.error,
  };
  const sizeMap: Record<string, number> = { sm: 12, md: 13, lg: 15 };
  return (
    <div style={{
      color: colorMap[variant ?? 'default'] ?? colors.fg,
      fontSize: sizeMap[size ?? 'md'] ?? 13,
      fontFamily: baseFontFamily,
      lineHeight: 1.5,
      whiteSpace: 'pre-wrap',
    }}>
      {content}
    </div>
  );
}

function StackComponent({ element, children }: ComponentRenderProps) {
  const { direction, gap, align } = element.props as {
    direction: string | null; gap: string | null; align: string | null;
  };
  const gapMap: Record<string, number> = { sm: 6, md: 12, lg: 20 };
  const isHorizontal = direction === 'horizontal';
  return (
    <div style={{
      display: 'flex',
      flexDirection: isHorizontal ? 'row' : 'column',
      gap: gapMap[gap ?? 'md'] ?? 12,
      alignItems: align ?? (isHorizontal ? 'center' : 'stretch'),
      flexWrap: isHorizontal ? 'wrap' : undefined,
    }}>
      {children}
    </div>
  );
}

function BadgeComponent({ element }: ComponentRenderProps) {
  const { label, variant } = element.props as { label: string; variant: string | null };
  const variantColors: Record<string, { bg: string; fg: string }> = {
    default: { bg: '#1e293b', fg: colors.fg },
    success: { bg: '#052e16', fg: colors.success },
    warning: { bg: '#422006', fg: colors.warning },
    error: { bg: '#450a0a', fg: colors.error },
    accent: { bg: '#0c4a6e', fg: colors.accent },
  };
  const v = variantColors[variant ?? 'default'] ?? variantColors.default;
  return (
    <span style={{
      display: 'inline-block', padding: '2px 8px', borderRadius: 4,
      fontSize: 11, fontWeight: 600, fontFamily: baseFontFamily,
      background: v.bg, color: v.fg, letterSpacing: '0.02em',
    }}>
      {label}
    </span>
  );
}

function MetricComponent({ element }: ComponentRenderProps) {
  const { label, value, change, trend } = element.props as {
    label: string; value: string; change: string | null; trend: string | null;
  };
  const trendColor =
    trend === 'up' ? colors.success :
    trend === 'down' ? colors.error :
    colors.muted;
  const trendArrow = trend === 'up' ? '\u2191' : trend === 'down' ? '\u2193' : '';
  return (
    <div style={{
      padding: '10px 14px', border: `1px solid ${colors.border}`,
      borderRadius: 6, background: colors.cardBg, fontFamily: baseFontFamily, minWidth: 120,
    }}>
      <div style={{ color: colors.muted, fontSize: 11, textTransform: 'uppercase', letterSpacing: '0.05em', marginBottom: 4 }}>
        {label}
      </div>
      <div style={{ color: colors.fg, fontSize: 22, fontWeight: 700 }}>{value}</div>
      {change && (
        <div style={{ color: trendColor, fontSize: 12, marginTop: 2 }}>
          {trendArrow} {change}
        </div>
      )}
    </div>
  );
}

function TweetPreviewComponent({ element }: ComponentRenderProps) {
  const { author, handle, content, timestamp, likes, retweets, replies } = element.props as {
    author: string; handle: string; content: string;
    timestamp: string | null; likes: string | null; retweets: string | null; replies: string | null;
  };
  return (
    <div style={{
      border: `1px solid ${colors.border}`, borderRadius: 8, padding: '12px 16px',
      background: colors.cardBg, fontFamily: baseFontFamily,
    }}>
      <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 8 }}>
        <div style={{
          width: 32, height: 32, borderRadius: '50%', background: colors.accent,
          display: 'flex', alignItems: 'center', justifyContent: 'center',
          color: colors.bg, fontWeight: 700, fontSize: 14,
        }}>
          {author.charAt(0).toUpperCase()}
        </div>
        <div>
          <div style={{ color: colors.fg, fontWeight: 700, fontSize: 13 }}>{author}</div>
          <div style={{ color: colors.muted, fontSize: 12 }}>@{handle}</div>
        </div>
        {timestamp && (
          <div style={{ color: colors.muted, fontSize: 11, marginLeft: 'auto' }}>{timestamp}</div>
        )}
      </div>
      <div style={{ color: colors.fg, fontSize: 14, lineHeight: 1.5, whiteSpace: 'pre-wrap', marginBottom: 10 }}>
        {content}
      </div>
      <div style={{ display: 'flex', gap: 20, color: colors.muted, fontSize: 12 }}>
        {replies != null && <span>{replies} replies</span>}
        {retweets != null && <span>{retweets} reposts</span>}
        {likes != null && <span>{likes} likes</span>}
      </div>
    </div>
  );
}

function AccountCardComponent({ element }: ComponentRenderProps) {
  const { name, handle, status, useCount, lastUsed } = element.props as {
    name: string; handle: string | null; status: string | null;
    useCount: string | null; lastUsed: string | null;
  };
  const statusColors: Record<string, string> = {
    active: colors.success, inactive: colors.muted, 'rate-limited': colors.warning,
  };
  const statusColor = statusColors[status ?? 'inactive'] ?? colors.muted;
  return (
    <div style={{
      border: `1px solid ${colors.border}`, borderRadius: 8, padding: '10px 14px',
      background: colors.cardBg, fontFamily: baseFontFamily,
    }}>
      <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginBottom: 6 }}>
        <div style={{ color: colors.fg, fontWeight: 700, fontSize: 14 }}>{name}</div>
        {status && (
          <span style={{
            display: 'inline-block', padding: '2px 8px', borderRadius: 4,
            fontSize: 11, fontWeight: 600, color: statusColor,
            background: `${statusColor}18`,
          }}>
            {status}
          </span>
        )}
      </div>
      {handle && <div style={{ color: colors.muted, fontSize: 12, marginBottom: 6 }}>@{handle}</div>}
      <div style={{ display: 'flex', gap: 16, color: colors.muted, fontSize: 12 }}>
        {useCount != null && <span>Used: {useCount}x</span>}
        {lastUsed != null && <span>Last: {lastUsed}</span>}
      </div>
    </div>
  );
}

function TableComponent({ element }: ComponentRenderProps) {
  const { headers, rows, caption } = element.props as {
    headers: string[]; rows: string[][]; caption: string | null;
  };
  return (
    <div style={{ fontFamily: baseFontFamily, overflow: 'auto' }}>
      {caption && <div style={{ color: colors.muted, fontSize: 12, marginBottom: 6 }}>{caption}</div>}
      <table style={{ width: '100%', borderCollapse: 'collapse', fontSize: 13 }}>
        <thead>
          <tr>
            {headers.map((h: string, i: number) => (
              <th key={i} style={{
                textAlign: 'left', padding: '6px 10px', color: colors.accent,
                borderBottom: `1px solid ${colors.border}`, fontWeight: 600,
              }}>
                {h}
              </th>
            ))}
          </tr>
        </thead>
        <tbody>
          {rows.map((row: string[], ri: number) => (
            <tr key={ri}>
              {row.map((cell: string, ci: number) => (
                <td key={ci} style={{
                  padding: '6px 10px', color: colors.fg,
                  borderBottom: '1px solid #1e293b',
                }}>
                  {cell}
                </td>
              ))}
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

function SeparatorComponent({ element }: ComponentRenderProps) {
  const { label } = element.props as { label: string | null };
  return (
    <div style={{ display: 'flex', alignItems: 'center', gap: 10, margin: '4px 0' }}>
      <div style={{ flex: 1, height: 1, background: '#1e293b' }} />
      {label && (
        <span style={{ color: colors.muted, fontSize: 11, textTransform: 'uppercase', letterSpacing: '0.05em' }}>
          {label}
        </span>
      )}
      <div style={{ flex: 1, height: 1, background: '#1e293b' }} />
    </div>
  );
}

function ButtonComponent({ element, onAction }: ComponentRenderProps) {
  const { label, variant, disabled } = element.props as {
    label: string; variant: string | null; disabled: boolean | null;
  };
  const isPrimary = variant === 'primary';
  const isGhost = variant === 'ghost';
  return (
    <button
      disabled={disabled ?? false}
      onClick={() => onAction?.({ name: 'press', params: {} })}
      style={{
        padding: '6px 14px', borderRadius: 6,
        border: isGhost ? 'none' : `1px solid ${isPrimary ? colors.accent : colors.border}`,
        background: isPrimary ? colors.accent : isGhost ? 'transparent' : colors.cardBg,
        color: isPrimary ? colors.bg : colors.fg,
        fontWeight: 600, fontSize: 13, fontFamily: baseFontFamily,
        cursor: disabled ? 'not-allowed' : 'pointer',
        opacity: disabled ? 0.5 : 1,
      }}
    >
      {label}
    </button>
  );
}

function CodeBlockComponent({ element }: ComponentRenderProps) {
  const { code, language } = element.props as { code: string; language: string | null };
  return (
    <div style={{
      background: '#0d1117', border: '1px solid #1e293b', borderRadius: 6,
      padding: '10px 14px', fontFamily: baseFontFamily, fontSize: 13,
      color: colors.fg, overflow: 'auto', whiteSpace: 'pre', lineHeight: 1.5,
    }}>
      {language && (
        <div style={{ color: colors.muted, fontSize: 11, marginBottom: 6, textTransform: 'uppercase' }}>
          {language}
        </div>
      )}
      <code>{code}</code>
    </div>
  );
}

function ProgressComponent({ element }: ComponentRenderProps) {
  const { value, max, label } = element.props as {
    value: number; max: number | null; label: string | null;
  };
  const maxVal = max ?? 100;
  const pct = Math.min(100, Math.max(0, (value / maxVal) * 100));
  return (
    <div style={{ fontFamily: baseFontFamily }}>
      {label && (
        <div style={{ display: 'flex', justifyContent: 'space-between', color: colors.muted, fontSize: 12, marginBottom: 4 }}>
          <span>{label}</span>
          <span>{Math.round(pct)}%</span>
        </div>
      )}
      <div style={{ height: 6, borderRadius: 3, background: '#1e293b', overflow: 'hidden' }}>
        <div style={{ height: '100%', borderRadius: 3, background: colors.accent, width: `${pct}%`, transition: 'width 0.3s ease' }} />
      </div>
    </div>
  );
}

export const registry: ComponentRegistry = {
  Card: CardComponent,
  Heading: HeadingComponent,
  Text: TextComponent,
  Stack: StackComponent,
  Badge: BadgeComponent,
  Metric: MetricComponent,
  TweetPreview: TweetPreviewComponent,
  AccountCard: AccountCardComponent,
  Table: TableComponent,
  Separator: SeparatorComponent,
  Button: ButtonComponent,
  CodeBlock: CodeBlockComponent,
  Progress: ProgressComponent,
};
