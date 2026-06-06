import type { AbstractConstructor, Mixin, TwitterClientBase } from './twitter-client-base.js';
import type { TweetFetchOptions } from './twitter-client-tweet-detail.js';
import type { SearchResult, TwitterUser } from './twitter-client-types.js';
export interface PostActivityUserResult {
    success: boolean;
    users?: TwitterUser[];
    nextCursor?: string;
    error?: string;
}
export interface PostActivityUserPaginationOptions {
    maxPages?: number;
    cursor?: string;
    pageDelayMs?: number;
}
export interface QuoteTweetsPaginationOptions extends TweetFetchOptions {
    maxPages?: number;
    cursor?: string;
    pageDelayMs?: number;
}
export interface TwitterClientPostActivityMethods {
    getFavoriters(tweetId: string, count?: number, cursor?: string): Promise<PostActivityUserResult>;
    getAllFavoriters(tweetId: string, options?: PostActivityUserPaginationOptions): Promise<PostActivityUserResult>;
    getRetweeters(tweetId: string, count?: number, cursor?: string): Promise<PostActivityUserResult>;
    getAllRetweeters(tweetId: string, options?: PostActivityUserPaginationOptions): Promise<PostActivityUserResult>;
    getQuoteTweets(tweetId: string, count?: number, options?: TweetFetchOptions & {
        cursor?: string;
    }): Promise<SearchResult>;
    getAllQuoteTweets(tweetId: string, options?: QuoteTweetsPaginationOptions): Promise<SearchResult>;
}
export declare function withPostActivity<TBase extends AbstractConstructor<TwitterClientBase>>(Base: TBase): Mixin<TBase, TwitterClientPostActivityMethods>;
//# sourceMappingURL=twitter-client-post-activity.d.ts.map
