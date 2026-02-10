import type { AbstractConstructor, Mixin, TwitterClientBase } from './twitter-client-base.js';
import type { BookmarkMutationResult } from './twitter-client-types.js';
export interface TwitterClientEngagementMethods {
    /** Like a tweet. */
    like(tweetId: string): Promise<BookmarkMutationResult>;
    /** Remove a like from a tweet. */
    unlike(tweetId: string): Promise<BookmarkMutationResult>;
    /** Retweet a tweet. */
    retweet(tweetId: string): Promise<BookmarkMutationResult>;
    /** Remove a retweet. */
    unretweet(tweetId: string): Promise<BookmarkMutationResult>;
    /** Bookmark a tweet. */
    bookmark(tweetId: string): Promise<BookmarkMutationResult>;
}
export declare function withEngagement<TBase extends AbstractConstructor<TwitterClientBase>>(Base: TBase): Mixin<TBase, TwitterClientEngagementMethods>;
//# sourceMappingURL=twitter-client-engagement.d.ts.map