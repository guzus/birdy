import type { AbstractConstructor, Mixin, TwitterClientBase } from './twitter-client-base.js';
import type { SearchResult } from './twitter-client-types.js';
/** Options for search methods */
export interface SearchFetchOptions {
    /** Include raw GraphQL response in `_raw` field */
    includeRaw?: boolean;
}
/** Options for paged search methods */
export interface SearchPaginationOptions extends SearchFetchOptions {
    maxPages?: number;
    /** Starting cursor for pagination (resume from previous fetch) */
    cursor?: string;
}
export interface TwitterClientSearchMethods {
    search(query: string, count?: number, options?: SearchFetchOptions): Promise<SearchResult>;
    getAllSearchResults(query: string, options?: SearchPaginationOptions): Promise<SearchResult>;
}
export declare function withSearch<TBase extends AbstractConstructor<TwitterClientBase>>(Base: TBase): Mixin<TBase, TwitterClientSearchMethods>;
//# sourceMappingURL=twitter-client-search.d.ts.map