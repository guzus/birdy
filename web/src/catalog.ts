import { createCatalog } from '@json-render/core';
import { z } from 'zod';

export const catalog = createCatalog({
  name: 'birdy',
  components: {
    Card: {
      props: z.object({
        title: z.string().nullable(),
        description: z.string().nullable(),
        variant: z.enum(['default', 'accent', 'muted']).nullable(),
      }),
      hasChildren: true,
      description: 'Container card with optional title and description',
    },
    Heading: {
      props: z.object({
        text: z.string(),
        level: z.enum(['1', '2', '3']).nullable(),
      }),
      description: 'Section heading',
    },
    Text: {
      props: z.object({
        content: z.string(),
        variant: z.enum(['default', 'muted', 'accent', 'error']).nullable(),
        size: z.enum(['sm', 'md', 'lg']).nullable(),
      }),
      description: 'Text block with optional styling',
    },
    Stack: {
      props: z.object({
        direction: z.enum(['vertical', 'horizontal']).nullable(),
        gap: z.enum(['sm', 'md', 'lg']).nullable(),
        align: z.enum(['start', 'center', 'end', 'stretch']).nullable(),
      }),
      hasChildren: true,
      description: 'Layout container that stacks children vertically or horizontally',
    },
    Badge: {
      props: z.object({
        label: z.string(),
        variant: z.enum(['default', 'success', 'warning', 'error', 'accent']).nullable(),
      }),
      description: 'Small status badge or tag',
    },
    Metric: {
      props: z.object({
        label: z.string(),
        value: z.string(),
        change: z.string().nullable(),
        trend: z.enum(['up', 'down', 'neutral']).nullable(),
      }),
      description: 'Single metric display with label, value, and optional trend',
    },
    TweetPreview: {
      props: z.object({
        author: z.string(),
        handle: z.string(),
        content: z.string(),
        timestamp: z.string().nullable(),
        likes: z.string().nullable(),
        retweets: z.string().nullable(),
        replies: z.string().nullable(),
      }),
      description: 'Preview card for a tweet/post with author info and engagement metrics',
    },
    AccountCard: {
      props: z.object({
        name: z.string(),
        handle: z.string().nullable(),
        status: z.enum(['active', 'inactive', 'rate-limited']).nullable(),
        useCount: z.string().nullable(),
        lastUsed: z.string().nullable(),
      }),
      description: 'Account summary card showing status and usage',
    },
    Table: {
      props: z.object({
        headers: z.array(z.string()),
        rows: z.array(z.array(z.string())),
        caption: z.string().nullable(),
      }),
      description: 'Data table with headers, rows, and optional caption',
    },
    Separator: {
      props: z.object({
        label: z.string().nullable(),
      }),
      description: 'Horizontal separator with optional label',
    },
    Button: {
      props: z.object({
        label: z.string(),
        variant: z.enum(['primary', 'secondary', 'ghost']).nullable(),
        disabled: z.boolean().nullable(),
      }),
      description: 'Interactive button',
    },
    CodeBlock: {
      props: z.object({
        code: z.string(),
        language: z.string().nullable(),
      }),
      description: 'Syntax-highlighted code block',
    },
    Progress: {
      props: z.object({
        value: z.number(),
        max: z.number().nullable(),
        label: z.string().nullable(),
      }),
      description: 'Progress bar with value and optional label',
    },
  },
  actions: {
    copyToClipboard: { description: 'Copy text content to clipboard' },
    dismiss: { description: 'Dismiss or close the current UI panel' },
  },
});
