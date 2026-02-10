import type { AbstractConstructor, Mixin, TwitterClientBase } from './twitter-client-base.js';
import type { SearchResult } from './twitter-client-types.js';
/** Options for user tweets fetch methods */
export interface UserTweetsFetchOptions {
    /** Include raw GraphQL response in `_raw` field */
    includeRaw?: boolean;
}
/** Options for paginated user tweets fetch */
export interface UserTweetsPaginationOptions extends UserTweetsFetchOptions {
    /** Maximum number of pages to fetch (default: 1) */
    maxPages?: number;
    /** Starting cursor for pagination (resume from previous fetch) */
    cursor?: string;
    /** Delay in milliseconds between page fetches (default: 1000) */
    pageDelayMs?: number;
}
export interface TwitterClientUserTweetsMethods {
    getUserTweets(userId: string, count?: number, options?: UserTweetsFetchOptions): Promise<SearchResult>;
    getUserTweetsPaged(userId: string, limit: number, options?: UserTweetsPaginationOptions): Promise<SearchResult>;
}
export declare function withUserTweets<TBase extends AbstractConstructor<TwitterClientBase>>(Base: TBase): Mixin<TBase, TwitterClientUserTweetsMethods>;
//# sourceMappingURL=twitter-client-user-tweets.d.ts.map