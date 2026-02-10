import type { Command } from 'commander';
import { type CookieSource, resolveCredentials } from '../lib/cookies.js';
import { labelPrefix, type OutputConfig, statusPrefix } from '../lib/output.js';
import type { TweetData } from '../lib/twitter-client.js';
export type BirdConfig = {
    chromeProfile?: string;
    chromeProfileDir?: string;
    firefoxProfile?: string;
    cookieSource?: CookieSource | CookieSource[];
    cookieTimeoutMs?: number;
    timeoutMs?: number;
    quoteDepth?: number;
};
export type MediaSpec = {
    path: string;
    alt?: string;
    mime: string;
    buffer: Buffer;
};
export type CliContext = {
    isTty: boolean;
    getOutput: () => OutputConfig;
    colors: {
        banner: (t: string) => string;
        subtitle: (t: string) => string;
        section: (t: string) => string;
        bullet: (t: string) => string;
        command: (t: string) => string;
        option: (t: string) => string;
        argument: (t: string) => string;
        description: (t: string) => string;
        muted: (t: string) => string;
        accent: (t: string) => string;
    };
    p: (kind: Parameters<typeof statusPrefix>[0]) => string;
    l: (kind: Parameters<typeof labelPrefix>[0]) => string;
    config: BirdConfig;
    applyOutputFromCommand: (command: Command) => void;
    resolveTimeoutFromOptions: (options: {
        timeout?: string | number;
    }) => number | undefined;
    resolveQuoteDepthFromOptions: (options: {
        quoteDepth?: string | number;
    }) => number | undefined;
    resolveCredentialsFromOptions: (opts: CredentialsOptions) => ReturnType<typeof resolveCredentials>;
    loadMedia: (opts: {
        media: string[];
        alts: string[];
    }) => MediaSpec[];
    printTweets: (tweets: TweetData[], opts?: {
        json?: boolean;
        emptyMessage?: string;
        showSeparator?: boolean;
    }) => void;
    printTweetsResult: (result: {
        tweets?: TweetData[];
        nextCursor?: string;
    }, opts: {
        json: boolean;
        usePagination: boolean;
        emptyMessage: string;
    }) => void;
    extractTweetId: (tweetIdOrUrl: string) => string;
};
export declare const collectCookieSource: (value: string, previous?: CookieSource[]) => CookieSource[];
type CredentialsOptions = {
    authToken?: string;
    ct0?: string;
    chromeProfile?: string;
    chromeProfileDir?: string;
    firefoxProfile?: string;
    cookieSource?: CookieSource[];
    cookieTimeout?: string | number;
};
export declare function createCliContext(normalizedArgs: string[], env?: NodeJS.ProcessEnv): CliContext;
export {};
//# sourceMappingURL=shared.d.ts.map