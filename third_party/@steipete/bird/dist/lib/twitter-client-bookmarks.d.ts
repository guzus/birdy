import type { AbstractConstructor, Mixin, TwitterClientBase } from './twitter-client-base.js';
import type { BookmarkMutationResult } from './twitter-client-types.js';
export interface TwitterClientBookmarkMethods {
    unbookmark(tweetId: string): Promise<BookmarkMutationResult>;
}
export declare function withBookmarks<TBase extends AbstractConstructor<TwitterClientBase>>(Base: TBase): Mixin<TBase, TwitterClientBookmarkMethods>;
//# sourceMappingURL=twitter-client-bookmarks.d.ts.map