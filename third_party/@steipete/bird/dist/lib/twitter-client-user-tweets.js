import { TWITTER_API_BASE } from './twitter-client-constants.js';
import { buildUserTweetsFeatures } from './twitter-client-features.js';
import { extractCursorFromInstructions, parseTweetsFromInstructions } from './twitter-client-utils.js';
export function withUserTweets(Base) {
    class TwitterClientUserTweets extends Base {
        // biome-ignore lint/complexity/noUselessConstructor lint/suspicious/noExplicitAny: TS mixin constructor requirement.
        constructor(...args) {
            super(...args);
        }
        async getUserTweetsQueryIds() {
            const primary = await this.getQueryId('UserTweets');
            // Fallback query ID observed from web client
            return Array.from(new Set([primary, 'Wms1GvIiHXAPBaCr9KblaA']));
        }
        /**
         * Get tweets from a user's profile timeline (single page).
         */
        async getUserTweets(userId, count = 20, options = {}) {
            return this.getUserTweetsPaged(userId, count, options);
        }
        /**
         * Get tweets from a user's profile timeline with pagination support.
         */
        async getUserTweetsPaged(userId, limit, options = {}) {
            if (!Number.isFinite(limit) || limit <= 0) {
                return { success: false, error: `Invalid limit: ${limit}` };
            }
            const { includeRaw = false, maxPages, pageDelayMs = 1000 } = options;
            const features = buildUserTweetsFeatures();
            const pageSize = 20;
            const seen = new Set();
            const tweets = [];
            let cursor = options.cursor;
            let nextCursor;
            let pagesFetched = 0;
            const hardMaxPages = 10;
            const computedMaxPages = Math.max(1, Math.ceil(limit / pageSize));
            const effectiveMaxPages = Math.min(hardMaxPages, maxPages ?? computedMaxPages);
            const fetchPage = async (pageCount, pageCursor) => {
                let lastError;
                let had404 = false;
                const queryIds = await this.getUserTweetsQueryIds();
                const variables = {
                    userId,
                    count: pageCount,
                    includePromotedContent: false, // Filter out ads
                    withQuickPromoteEligibilityTweetFields: true,
                    withVoice: true,
                    ...(pageCursor ? { cursor: pageCursor } : {}),
                };
                const fieldToggles = {
                    withArticlePlainText: false,
                };
                const params = new URLSearchParams({
                    variables: JSON.stringify(variables),
                    features: JSON.stringify(features),
                    fieldToggles: JSON.stringify(fieldToggles),
                });
                for (const queryId of queryIds) {
                    const url = `${TWITTER_API_BASE}/${queryId}/UserTweets?${params.toString()}`;
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
                            // Check for common errors
                            const errorMsg = data.errors.map((e) => e.message).join(', ');
                            if (errorMsg.includes('User has been suspended') || errorMsg.includes('User not found')) {
                                return { success: false, error: errorMsg, had404 };
                            }
                            // Some errors are non-fatal if we got data
                            if (!data.data?.user?.result?.timeline?.timeline?.instructions) {
                                return { success: false, error: errorMsg, had404 };
                            }
                        }
                        const instructions = data.data?.user?.result?.timeline?.timeline?.instructions;
                        const pageTweets = parseTweetsFromInstructions(instructions, { quoteDepth: this.quoteDepth, includeRaw });
                        const pageCursorValue = extractCursorFromInstructions(instructions);
                        return { success: true, tweets: pageTweets, cursor: pageCursorValue, had404 };
                    }
                    catch (error) {
                        lastError = error instanceof Error ? error.message : String(error);
                    }
                }
                return { success: false, error: lastError ?? 'Unknown error fetching user tweets', had404 };
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
            while (tweets.length < limit) {
                // Add delay between pages (but not before the first page)
                if (pagesFetched > 0 && pageDelayMs > 0) {
                    await this.sleep(pageDelayMs);
                }
                const remaining = limit - tweets.length;
                const pageCount = Math.min(pageSize, remaining);
                const page = await fetchWithRefresh(pageCount, cursor);
                if (!page.success) {
                    return { success: false, error: page.error };
                }
                pagesFetched += 1;
                let added = 0;
                for (const tweet of page.tweets) {
                    if (seen.has(tweet.id)) {
                        continue;
                    }
                    seen.add(tweet.id);
                    tweets.push(tweet);
                    added += 1;
                    if (tweets.length >= limit) {
                        break;
                    }
                }
                const pageCursor = page.cursor;
                if (!pageCursor || pageCursor === cursor || page.tweets.length === 0 || added === 0) {
                    nextCursor = undefined;
                    break;
                }
                if (pagesFetched >= effectiveMaxPages) {
                    nextCursor = pageCursor;
                    break;
                }
                cursor = pageCursor;
                nextCursor = pageCursor;
            }
            return { success: true, tweets, nextCursor };
        }
    }
    return TwitterClientUserTweets;
}
//# sourceMappingURL=twitter-client-user-tweets.js.map