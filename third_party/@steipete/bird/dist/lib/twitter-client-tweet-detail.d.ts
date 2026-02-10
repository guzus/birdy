import type { AbstractConstructor, Mixin, TwitterClientBase } from './twitter-client-base.js';
import type { GetTweetResult, SearchResult } from './twitter-client-types.js';
/** Options for tweet fetching methods */
export interface TweetFetchOptions {
    /** Include raw GraphQL response in `_raw` field */
    includeRaw?: boolean;
}
/** Options for paginated tweet detail fetch */
export interface TweetDetailPaginationOptions extends TweetFetchOptions {
    /** Maximum number of pages to fetch (default: unlimited when using pagination) */
    maxPages?: number;
    /** Starting cursor for pagination (resume from previous fetch) */
    cursor?: string;
    /** Delay in milliseconds between page fetches (default: 1000) */
    pageDelayMs?: number;
}
export interface TwitterClientTweetDetailMethods {
    getTweet(tweetId: string, options?: TweetFetchOptions): Promise<GetTweetResult>;
    getReplies(tweetId: string, options?: TweetFetchOptions): Promise<SearchResult>;
    getThread(tweetId: string, options?: TweetFetchOptions): Promise<SearchResult>;
    getRepliesPaged(tweetId: string, options?: TweetDetailPaginationOptions): Promise<SearchResult>;
    getThreadPaged(tweetId: string, options?: TweetDetailPaginationOptions): Promise<SearchResult>;
}
export declare function withTweetDetails<TBase extends AbstractConstructor<TwitterClientBase>>(Base: TBase): Mixin<TBase, TwitterClientTweetDetailMethods>;
//# sourceMappingURL=twitter-client-tweet-detail.d.ts.map