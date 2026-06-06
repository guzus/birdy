import { TWITTER_API_BASE } from './twitter-client-constants.js';
import { buildSearchFeatures, buildTimelineFeatures } from './twitter-client-features.js';
import { extractCursorFromInstructions, parseTweetsFromInstructions, parseUsersFromInstructions, } from './twitter-client-utils.js';
export function withPostActivity(Base) {
    class TwitterClientPostActivity extends Base {
        // biome-ignore lint/complexity/noUselessConstructor lint/suspicious/noExplicitAny: TS mixin constructor requirement.
        constructor(...args) {
            super(...args);
        }
        async getFavoritersQueryIds() {
            const primary = await this.getQueryId('Favoriters');
            return Array.from(new Set([primary, 'Vm_Xdlz2IgZYOuNZyXv6CQ'].filter(Boolean)));
        }
        async getRetweetersQueryIds() {
            const primary = await this.getQueryId('Retweeters');
            return Array.from(new Set([primary, 'qtic8qdylD9Q5DtNrF00qg'].filter(Boolean)));
        }
        async fetchPostActivityUsers(operation, tweetId, count = 20, cursor) {
            const features = buildTimelineFeatures();
            const variables = {
                tweetId,
                count,
                enableRanking: true,
                includePromotedContent: true,
                ...(cursor ? { cursor } : {}),
            };
            const params = new URLSearchParams({
                variables: JSON.stringify(variables),
                features: JSON.stringify(features),
            });
            const queryIds = operation === 'Favoriters' ? await this.getFavoritersQueryIds() : await this.getRetweetersQueryIds();
            const dataKey = operation === 'Favoriters' ? 'favoriters_timeline' : 'retweeters_timeline';
            let lastError;
            let had404 = false;
            for (const queryId of queryIds) {
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
                        return { success: false, error: data.errors.map((e) => e.message).join(', '), had404 };
                    }
                    const instructions = data.data?.[dataKey]?.timeline?.instructions;
                    const users = parseUsersFromInstructions(instructions);
                    const nextCursor = extractCursorFromInstructions(instructions);
                    return { success: true, users, nextCursor, had404 };
                }
                catch (error) {
                    lastError = error instanceof Error ? error.message : String(error);
                }
            }
            return { success: false, error: lastError ?? `Unknown error fetching ${operation}`, had404 };
        }
        async getFavoriters(tweetId, count = 20, cursor) {
            const tryOnce = () => this.fetchPostActivityUsers('Favoriters', tweetId, count, cursor);
            const { result } = await this.withRefreshedQueryIdsOn404(tryOnce);
            return result.success
                ? { success: true, users: result.users, nextCursor: result.nextCursor }
                : { success: false, error: result.error };
        }
        async getRetweeters(tweetId, count = 20, cursor) {
            const tryOnce = () => this.fetchPostActivityUsers('Retweeters', tweetId, count, cursor);
            const { result } = await this.withRefreshedQueryIdsOn404(tryOnce);
            return result.success
                ? { success: true, users: result.users, nextCursor: result.nextCursor }
                : { success: false, error: result.error };
        }
        async getAllPostActivityUsers(fetchPage, options = {}) {
            const seen = new Set();
            const users = [];
            let cursor = options.cursor;
            let nextCursor;
            let pagesFetched = 0;
            const pageDelayMs = options.pageDelayMs ?? 1000;
            while (true) {
                const page = await fetchPage(cursor);
                if (!page.success || !page.users) {
                    return users.length > 0
                        ? { success: false, users, nextCursor, error: page.error }
                        : { success: false, error: page.error };
                }
                pagesFetched += 1;
                let added = 0;
                for (const user of page.users) {
                    if (seen.has(user.id)) {
                        continue;
                    }
                    seen.add(user.id);
                    users.push(user);
                    added += 1;
                }
                const pageCursor = page.nextCursor;
                if (!pageCursor || pageCursor === cursor || page.users.length === 0 || added === 0) {
                    nextCursor = undefined;
                    break;
                }
                if (options.maxPages && pagesFetched >= options.maxPages) {
                    nextCursor = pageCursor;
                    break;
                }
                cursor = pageCursor;
                nextCursor = pageCursor;
                await this.sleep(pageDelayMs);
            }
            return { success: true, users, nextCursor };
        }
        async getAllFavoriters(tweetId, options = {}) {
            return this.getAllPostActivityUsers((cursor) => this.getFavoriters(tweetId, 20, cursor), options);
        }
        async getAllRetweeters(tweetId, options = {}) {
            return this.getAllPostActivityUsers((cursor) => this.getRetweeters(tweetId, 20, cursor), options);
        }
        async fetchQuoteTweetsPage(tweetId, count = 20, options = {}) {
            const features = buildSearchFeatures();
            const variables = {
                rawQuery: `quoted_tweet_id:${tweetId}`,
                count,
                querySource: 'tdqt',
                product: 'Top',
                withGrokTranslatedBio: true,
                withQuickPromoteEligibilityTweetFields: false,
                ...(options.cursor ? { cursor: options.cursor } : {}),
            };
            const params = new URLSearchParams({
                variables: JSON.stringify(variables),
            });
            const queryIds = Array.from(new Set(['dsWn-Op2S0SmJjgY6Yvckg', ...(await this.getSearchTimelineQueryIds())]));
            let lastError;
            let had404 = false;
            for (const queryId of queryIds) {
                const url = `${TWITTER_API_BASE}/${queryId}/SearchTimeline?${params.toString()}`;
                try {
                    const response = await this.fetchWithTimeout(url, {
                        method: 'POST',
                        headers: this.getHeaders(),
                        body: JSON.stringify({ features, queryId }),
                    });
                    if (response.status === 404) {
                        had404 = true;
                        lastError = `HTTP ${response.status}`;
                        continue;
                    }
                    if (!response.ok) {
                        const text = await response.text();
                        lastError = `HTTP ${response.status}: ${text.slice(0, 200)}`;
                        continue;
                    }
                    const data = (await response.json());
                    if (data.errors && data.errors.length > 0) {
                        const shouldRefreshQueryIds = data.errors.some((error) => error?.extensions?.code === 'GRAPHQL_VALIDATION_FAILED');
                        if (shouldRefreshQueryIds) {
                            had404 = true;
                        }
                        lastError = data.errors.map((e) => e.message).join(', ');
                        continue;
                    }
                    const instructions = data.data?.search_by_raw_query?.search_timeline?.timeline?.instructions;
                    const tweets = parseTweetsFromInstructions(instructions, {
                        quoteDepth: this.quoteDepth,
                        includeRaw: options.includeRaw,
                    });
                    const nextCursor = extractCursorFromInstructions(instructions);
                    return { success: true, tweets, nextCursor, had404 };
                }
                catch (error) {
                    lastError = error instanceof Error ? error.message : String(error);
                }
            }
            return { success: false, error: lastError ?? 'Unknown error fetching quote tweets', had404 };
        }
        async getQuoteTweets(tweetId, count = 20, options = {}) {
            const tryOnce = () => this.fetchQuoteTweetsPage(tweetId, count, options);
            const { result } = await this.withRefreshedQueryIdsOn404(tryOnce);
            return result.success
                ? { success: true, tweets: result.tweets, nextCursor: result.nextCursor }
                : { success: false, error: result.error };
        }
        async getAllQuoteTweets(tweetId, options = {}) {
            const seen = new Set();
            const tweets = [];
            let cursor = options.cursor;
            let nextCursor;
            let pagesFetched = 0;
            const pageDelayMs = options.pageDelayMs ?? 1000;
            while (true) {
                const page = await this.getQuoteTweets(tweetId, 20, { ...options, cursor });
                if (!page.success || !page.tweets) {
                    return tweets.length > 0
                        ? { success: false, tweets, nextCursor, error: page.error }
                        : { success: false, error: page.error };
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
                }
                const pageCursor = page.nextCursor;
                if (!pageCursor || pageCursor === cursor || page.tweets.length === 0 || added === 0) {
                    nextCursor = undefined;
                    break;
                }
                if (options.maxPages && pagesFetched >= options.maxPages) {
                    nextCursor = pageCursor;
                    break;
                }
                cursor = pageCursor;
                nextCursor = pageCursor;
                await this.sleep(pageDelayMs);
            }
            return { success: true, tweets, nextCursor };
        }
    }
    return TwitterClientPostActivity;
}
//# sourceMappingURL=twitter-client-post-activity.js.map
