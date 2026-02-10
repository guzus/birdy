import { TwitterClientBase } from './twitter-client-base.js';
import { withBookmarks } from './twitter-client-bookmarks.js';
import { withEngagement } from './twitter-client-engagement.js';
import { withFollow } from './twitter-client-follow.js';
import { withHome } from './twitter-client-home.js';
import { withLists } from './twitter-client-lists.js';
import { withMedia } from './twitter-client-media.js';
import { withNews } from './twitter-client-news.js';
import { withPosting } from './twitter-client-posting.js';
import { withSearch } from './twitter-client-search.js';
import { withTimelines } from './twitter-client-timelines.js';
import { withTweetDetails } from './twitter-client-tweet-detail.js';
import { withUserLookup } from './twitter-client-user-lookup.js';
import { withUserTweets } from './twitter-client-user-tweets.js';
import { withUsers } from './twitter-client-users.js';
// News mixin wraps search because it depends on the search() method
// Engagement mixin adds like/unlike/retweet/unretweet/bookmark methods
const MixedTwitterClient = withNews(withUserTweets(withUserLookup(withUsers(withLists(withHome(withTimelines(withSearch(withTweetDetails(withPosting(withEngagement(withFollow(withBookmarks(withMedia(TwitterClientBase))))))))))))));
export class TwitterClient extends MixedTwitterClient {
}
//# sourceMappingURL=twitter-client.js.map