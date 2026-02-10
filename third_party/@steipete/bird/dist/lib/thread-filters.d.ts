import type { TweetData, TweetWithMeta } from './twitter-client-types.js';
export declare function filterAuthorChain(tweets: TweetData[], bookmarkedTweet: TweetData): TweetData[];
export declare function filterAuthorOnly(tweets: TweetData[], bookmarkedTweet: TweetData): TweetData[];
export declare function filterFullChain(tweets: TweetData[], bookmarkedTweet: TweetData, options?: {
    includeAncestorBranches?: boolean;
}): TweetData[];
export declare function addThreadMetadata(tweet: TweetData, allConversationTweets: TweetData[]): TweetWithMeta;
//# sourceMappingURL=thread-filters.d.ts.map