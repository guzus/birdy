import { TWITTER_API_BASE } from './twitter-client-constants.js';
import { buildHomeTimelineFeatures } from './twitter-client-features.js';
import { extractCursorFromInstructions, parseTweetsFromInstructions } from './twitter-client-utils.js';
const QUERY_UNSPECIFIED_REGEX = /query:\s*unspecified/i;
function isQueryIdMismatch(errors) {
    return errors.some((error) => QUERY_UNSPECIFIED_REGEX.test(error.message ?? ''));
}
export function withHome(Base) {
    class TwitterClientHome extends Base {
        // biome-ignore lint/complexity/noUselessConstructor lint/suspicious/noExplicitAny: TS mixin constructor requirement.
        constructor(...args) {
            super(...args);
        }
        async getHomeTimelineQueryIds() {
            const primary = await this.getQueryId('HomeTimeline');
            return Array.from(new Set([primary, 'edseUwk9sP5Phz__9TIRnA']));
        }
        async getHomeLatestTimelineQueryIds() {
            const primary = await this.getQueryId('HomeLatestTimeline');
            return Array.from(new Set([primary, 'iOEZpOdfekFsxSlPQCQtPg']));
        }
        /**
         * Get the authenticated user's "For You" home timeline
         */
        async getHomeTimeline(count = 20, options = {}) {
            return this.fetchHomeTimeline('HomeTimeline', count, options);
        }
        /**
         * Get the authenticated user's "Following" (latest/chronological) home timeline
         */
        async getHomeLatestTimeline(count = 20, options = {}) {
            return this.fetchHomeTimeline('HomeLatestTimeline', count, options);
        }
        async fetchHomeTimeline(operation, count, options) {
            const { includeRaw = false } = options;
            const features = buildHomeTimelineFeatures();
            const pageSize = 20;
            const seen = new Set();
            const tweets = [];
            let cursor;
            const fetchPage = async (pageCount, pageCursor) => {
                let lastError;
                let had404 = false;
                const queryIds = operation === 'HomeTimeline'
                    ? await this.getHomeTimelineQueryIds()
                    : await this.getHomeLatestTimelineQueryIds();
                for (const queryId of queryIds) {
                    const variables = {
                        count: pageCount,
                        includePromotedContent: true,
                        latestControlAvailable: true,
                        requestContext: 'launch',
                        withCommunity: true,
                        ...(pageCursor ? { cursor: pageCursor } : {}),
                    };
                    const params = new URLSearchParams({
                        variables: JSON.stringify(variables),
                        features: JSON.stringify(features),
                    });
                    const url = `${TWITTER_API_BASE}/${queryId}/${operation}?${params.toString()}`;
                    try {
                        const response = await this.fetchWithTimeout(url, {
                            method: 'GET',
                            headers: this.getHeaders(),
                        });
                        if (response.status === 404) {
                            had404 = true;
                            lastError = `HTTP ${response.status}`;
                            continue;
                        }
                        if (!response.ok) {
                            const text = await response.text();
                            return { success: false, error: `HTTP ${response.status}: ${text.slice(0, 200)}`, had404 };
                        }
                        const data = (await response.json());
                        if (data.errors && data.errors.length > 0) {
                            const errorMessage = data.errors.map((e) => e.message).join(', ');
                            return {
                                success: false,
                                error: errorMessage,
                                had404: had404 || isQueryIdMismatch(data.errors),
                            };
                        }
                        const instructions = data.data?.home?.home_timeline_urt?.instructions;
                        const pageTweets = parseTweetsFromInstructions(instructions, { quoteDepth: this.quoteDepth, includeRaw });
                        const nextCursor = extractCursorFromInstructions(instructions);
                        return { success: true, tweets: pageTweets, cursor: nextCursor, had404 };
                    }
                    catch (error) {
                        lastError = error instanceof Error ? error.message : String(error);
                    }
                }
                return { success: false, error: lastError ?? 'Unknown error fetching home timeline', had404 };
            };
            const fetchWithRefresh = async (pageCount, pageCursor) => {
                const firstAttempt = await fetchPage(pageCount, pageCursor);
                if (firstAttempt.success) {
                    return firstAttempt;
                }
                if (firstAttempt.had404) {
                    await this.refreshQueryIds();
                    const secondAttempt = await fetchPage(pageCount, pageCursor);
                    if (secondAttempt.success) {
                        return secondAttempt;
                    }
                    return { success: false, error: secondAttempt.error };
                }
                return { success: false, error: firstAttempt.error };
            };
            while (tweets.length < count) {
                const pageCount = Math.min(pageSize, count - tweets.length);
                const page = await fetchWithRefresh(pageCount, cursor);
                if (!page.success) {
                    return { success: false, error: page.error };
                }
                let added = 0;
                for (const tweet of page.tweets) {
                    if (seen.has(tweet.id)) {
                        continue;
                    }
                    seen.add(tweet.id);
                    tweets.push(tweet);
                    added += 1;
                    if (tweets.length >= count) {
                        break;
                    }
                }
                if (!page.cursor || page.cursor === cursor || page.tweets.length === 0 || added === 0) {
                    break;
                }
                cursor = page.cursor;
            }
            return { success: true, tweets };
        }
    }
    return TwitterClientHome;
}
//# sourceMappingURL=twitter-client-home.js.map