import type { AbstractConstructor, Mixin, TwitterClientBase } from './twitter-client-base.js';
import type { TweetResult } from './twitter-client-types.js';
export interface TwitterClientPostingMethods {
    tweet(text: string, mediaIds?: string[]): Promise<TweetResult>;
    reply(text: string, replyToTweetId: string, mediaIds?: string[]): Promise<TweetResult>;
}
export declare function withPosting<TBase extends AbstractConstructor<TwitterClientBase>>(Base: TBase): Mixin<TBase, TwitterClientPostingMethods>;
//# sourceMappingURL=twitter-client-posting.d.ts.map