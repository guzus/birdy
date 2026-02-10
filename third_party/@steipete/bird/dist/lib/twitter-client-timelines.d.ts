import type { AbstractConstructor, Mixin, TwitterClientBase } from './twitter-client-base.js';
import type { SearchResult } from './twitter-client-types.js';
/** Options for timeline fetch methods */
export interface TimelineFetchOptions {
    /** Include raw GraphQL response in `_raw` field */
    includeRaw?: boolean;
}
/** Options for paged timeline fetch methods */
export interface TimelinePaginationOptions extends TimelineFetchOptions {
    maxPages?: number;
    /** Starting cursor for pagination (resume from previous fetch) */
    cursor?: string;
}
export interface TwitterClientTimelineMethods {
    getBookmarks(count?: number, options?: TimelineFetchOptions): Promise<SearchResult>;
    getAllBookmarks(options?: TimelinePaginationOptions): Promise<SearchResult>;
    getLikes(count?: number, options?: TimelineFetchOptions): Promise<SearchResult>;
    getAllLikes(options?: TimelinePaginationOptions): Promise<SearchResult>;
    getBookmarkFolderTimeline(folderId: string, count?: number, options?: TimelineFetchOptions): Promise<SearchResult>;
    getAllBookmarkFolderTimeline(folderId: string, options?: TimelinePaginationOptions): Promise<SearchResult>;
}
export declare function withTimelines<TBase extends AbstractConstructor<TwitterClientBase>>(Base: TBase): Mixin<TBase, TwitterClientTimelineMethods>;
//# sourceMappingURL=twitter-client-timelines.d.ts.map