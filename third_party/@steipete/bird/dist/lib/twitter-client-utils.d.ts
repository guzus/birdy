import type { GraphqlTweetResult, TweetData, TweetMedia, TwitterUser } from './twitter-client-types.js';
export declare function normalizeQuoteDepth(value?: number): number;
export declare function firstText(...values: Array<string | undefined | null>): string | undefined;
export declare function collectTextFields(value: unknown, keys: Set<string>, output: string[]): void;
export declare function uniqueOrdered(values: string[]): string[];
/** Inline style range for text formatting (Bold, Italic, etc.) */
interface InlineStyleRange {
    offset: number;
    length: number;
    style: string;
}
/** Entity range linking a portion of text to an entity in entityMap */
interface EntityRange {
    key: number;
    offset: number;
    length: number;
}
/** A content block in Draft.js format */
interface ContentBlock {
    key: string;
    type: string;
    text: string;
    data?: {
        mentions?: Array<{
            fromIndex: number;
            toIndex: number;
            text: string;
        }>;
    };
    entityRanges?: EntityRange[];
    inlineStyleRanges?: InlineStyleRange[];
}
/** Entity data for different entity types */
interface EntityValue {
    type: string;
    mutability: string;
    data: {
        markdown?: string;
        url?: string;
        tweetId?: string;
    };
}
/** Entity map entry */
interface EntityMapEntry {
    key: string;
    value: EntityValue;
}
/** Draft.js content state structure */
interface ContentState {
    blocks: ContentBlock[];
    entityMap?: Array<EntityMapEntry> | Record<string, EntityValue>;
}
/**
 * Renders a Draft.js content_state into readable markdown/text format.
 * Handles blocks (paragraphs, headers, lists) and entities (code blocks, links, tweets, dividers).
 */
export declare function renderContentState(contentState: ContentState | undefined): string | undefined;
export declare function extractArticleText(result: GraphqlTweetResult | undefined): string | undefined;
export declare function extractNoteTweetText(result: GraphqlTweetResult | undefined): string | undefined;
export declare function extractTweetText(result: GraphqlTweetResult | undefined): string | undefined;
export declare function extractArticleMetadata(result: GraphqlTweetResult | undefined): {
    title: string;
    previewText?: string;
} | undefined;
export declare function extractMedia(result: GraphqlTweetResult | undefined): TweetMedia[] | undefined;
export declare function unwrapTweetResult(result: GraphqlTweetResult | undefined): GraphqlTweetResult | undefined;
export interface MapTweetResultOptions {
    quoteDepth: number;
    includeRaw?: boolean;
}
export declare function mapTweetResult(result: GraphqlTweetResult | undefined, quoteDepthOrOptions: number | MapTweetResultOptions): TweetData | undefined;
export declare function findTweetInInstructions(instructions: Array<{
    entries?: Array<{
        content?: {
            itemContent?: {
                tweet_results?: {
                    result?: GraphqlTweetResult;
                };
            };
        };
    }>;
}> | undefined, tweetId: string): GraphqlTweetResult | undefined;
export declare function collectTweetResultsFromEntry(entry: {
    content?: {
        itemContent?: {
            tweet_results?: {
                result?: GraphqlTweetResult;
            };
        };
        item?: {
            itemContent?: {
                tweet_results?: {
                    result?: GraphqlTweetResult;
                };
            };
        };
        items?: Array<{
            item?: {
                itemContent?: {
                    tweet_results?: {
                        result?: GraphqlTweetResult;
                    };
                };
            };
            itemContent?: {
                tweet_results?: {
                    result?: GraphqlTweetResult;
                };
            };
            content?: {
                itemContent?: {
                    tweet_results?: {
                        result?: GraphqlTweetResult;
                    };
                };
            };
        }>;
    };
}): GraphqlTweetResult[];
export interface ParseTweetsOptions {
    quoteDepth: number;
    includeRaw?: boolean;
}
export declare function parseTweetsFromInstructions(instructions: Array<{
    entries?: Array<{
        content?: {
            itemContent?: {
                tweet_results?: {
                    result?: GraphqlTweetResult;
                };
            };
            item?: {
                itemContent?: {
                    tweet_results?: {
                        result?: GraphqlTweetResult;
                    };
                };
            };
            items?: Array<{
                item?: {
                    itemContent?: {
                        tweet_results?: {
                            result?: GraphqlTweetResult;
                        };
                    };
                };
                itemContent?: {
                    tweet_results?: {
                        result?: GraphqlTweetResult;
                    };
                };
                content?: {
                    itemContent?: {
                        tweet_results?: {
                            result?: GraphqlTweetResult;
                        };
                    };
                };
            }>;
        };
    }>;
}> | undefined, quoteDepthOrOptions: number | ParseTweetsOptions): TweetData[];
export declare function extractCursorFromInstructions(instructions: Array<{
    entries?: Array<{
        content?: unknown;
    }>;
}> | undefined, cursorType?: string): string | undefined;
export declare function parseUsersFromInstructions(instructions: Array<{
    type?: string;
    entries?: Array<unknown>;
}> | undefined): TwitterUser[];
export {};
//# sourceMappingURL=twitter-client-utils.d.ts.map