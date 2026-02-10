import type { AbstractConstructor } from './twitter-client-base.js';
import { TwitterClientBase } from './twitter-client-base.js';
import { type TwitterClientBookmarkMethods } from './twitter-client-bookmarks.js';
import { type TwitterClientEngagementMethods } from './twitter-client-engagement.js';
import { type TwitterClientFollowMethods } from './twitter-client-follow.js';
import { type TwitterClientHomeMethods } from './twitter-client-home.js';
import { type TwitterClientListMethods } from './twitter-client-lists.js';
import { type TwitterClientMediaMethods } from './twitter-client-media.js';
import { type TwitterClientNewsMethods } from './twitter-client-news.js';
import { type TwitterClientPostingMethods } from './twitter-client-posting.js';
import { type TwitterClientSearchMethods } from './twitter-client-search.js';
import { type TwitterClientTimelineMethods } from './twitter-client-timelines.js';
import { type TwitterClientTweetDetailMethods } from './twitter-client-tweet-detail.js';
import { type TwitterClientUserLookupMethods } from './twitter-client-user-lookup.js';
import { type TwitterClientUserTweetsMethods } from './twitter-client-user-tweets.js';
import { type TwitterClientUserMethods } from './twitter-client-users.js';
type TwitterClientInstance = TwitterClientBase & TwitterClientBookmarkMethods & TwitterClientEngagementMethods & TwitterClientFollowMethods & TwitterClientHomeMethods & TwitterClientListMethods & TwitterClientMediaMethods & TwitterClientNewsMethods & TwitterClientPostingMethods & TwitterClientSearchMethods & TwitterClientTimelineMethods & TwitterClientTweetDetailMethods & TwitterClientUserMethods & TwitterClientUserLookupMethods & TwitterClientUserTweetsMethods;
declare const MixedTwitterClient: AbstractConstructor<TwitterClientInstance>;
export declare class TwitterClient extends MixedTwitterClient {
}
export type { NewsFetchOptions, NewsItem, NewsResult } from './twitter-client-news.js';
export type { BookmarkMutationResult, CurrentUserResult, FollowingResult, FollowMutationResult, GetTweetResult, ListsResult, SearchResult, TweetData, TweetResult, TwitterClientOptions, TwitterList, TwitterUser, UploadMediaResult, } from './twitter-client-types.js';
//# sourceMappingURL=twitter-client.d.ts.map