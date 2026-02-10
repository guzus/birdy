import type { AbstractConstructor, Mixin, TwitterClientBase } from './twitter-client-base.js';
import type { TweetData } from './twitter-client-types.js';
declare const TIMELINE_IDS: {
    readonly forYou: "VGltZWxpbmU6DAC2CwABAAAAB2Zvcl95b3UAAA==";
    readonly trending: "VGltZWxpbmU6DAC2CwABAAAACHRyZW5kaW5nAAA=";
    readonly news: "VGltZWxpbmU6DAC2CwABAAAABG5ld3MAAA==";
    readonly sports: "VGltZWxpbmU6DAC2CwABAAAABnNwb3J0cwAA";
    readonly entertainment: "VGltZWxpbmU6DAC2CwABAAAADWVudGVydGFpbm1lbnQAAA==";
};
export type ExploreTab = keyof typeof TIMELINE_IDS;
/** Options for news fetch methods */
export interface NewsFetchOptions {
    /** Include raw GraphQL response in `_raw` field */
    includeRaw?: boolean;
    /** Also fetch related tweets for each news item */
    withTweets?: boolean;
    /** Number of tweets to fetch per news item (default: 5) */
    tweetsPerItem?: number;
    /** Filter to show only AI-curated news items */
    aiOnly?: boolean;
    /** Fetch from specific tabs only (default: all tabs) */
    tabs?: ExploreTab[];
}
export interface NewsItem {
    id: string;
    headline: string;
    category?: string;
    timeAgo?: string;
    postCount?: number;
    description?: string;
    url?: string;
    tweets?: TweetData[];
    _raw?: any;
}
export type NewsResult = {
    success: true;
    items: NewsItem[];
} | {
    success: false;
    error: string;
};
export interface TwitterClientNewsMethods {
    getNews(count?: number, options?: NewsFetchOptions): Promise<NewsResult>;
}
export declare function withNews<TBase extends AbstractConstructor<TwitterClientBase>>(Base: TBase): Mixin<TBase, TwitterClientNewsMethods>;
export {};
//# sourceMappingURL=twitter-client-news.d.ts.map