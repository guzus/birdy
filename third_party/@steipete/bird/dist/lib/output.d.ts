export type OutputConfig = {
    plain: boolean;
    emoji: boolean;
    color: boolean;
    hyperlinks: boolean;
};
export type StatusKind = 'ok' | 'warn' | 'err' | 'info' | 'hint';
export type LabelKind = 'url' | 'date' | 'source' | 'engine' | 'credentials' | 'user' | 'userId' | 'email';
export declare function resolveOutputConfigFromArgv(argv: string[], env: NodeJS.ProcessEnv, isTty: boolean): OutputConfig;
export declare function resolveOutputConfigFromCommander(opts: {
    plain?: boolean;
    emoji?: boolean;
    color?: boolean;
}, env: NodeJS.ProcessEnv, isTty: boolean): OutputConfig;
export declare function statusPrefix(kind: StatusKind, cfg: OutputConfig): string;
export declare function labelPrefix(kind: LabelKind, cfg: OutputConfig): string;
export declare function formatStatsLine(stats: {
    likeCount?: number | null;
    retweetCount?: number | null;
    replyCount?: number | null;
}, cfg: OutputConfig): string;
export declare function formatTweetUrl(tweetId: string): string;
/**
 * Wraps a URL in OSC 8 escape sequences to make it clickable in supported terminals.
 * Falls back to plain text when not in a TTY or when hyperlinks are disabled.
 */
export declare function hyperlink(url: string, text?: string, cfg?: OutputConfig): string;
export declare function formatTweetUrlLine(tweetId: string, cfg: OutputConfig): string;
//# sourceMappingURL=output.d.ts.map