import { TWITTER_API_BASE } from './twitter-client-constants.js';
import { buildBookmarksFeatures, buildLikesFeatures } from './twitter-client-features.js';
import { extractCursorFromInstructions, parseTweetsFromInstructions } from './twitter-client-utils.js';
export function withTimelines(Base) {
    class TwitterClientTimelines extends Base {
        // biome-ignore lint/complexity/noUselessConstructor lint/suspicious/noExplicitAny: TS mixin constructor requirement.
        constructor(...args) {
            super(...args);
        }
        logBookmarksDebug(message, data) {
            if (process.env.BIRD_DEBUG_BOOKMARKS !== '1') {
                return;
            }
            if (data) {
                console.error(`[bird][debug][bookmarks] ${message}`, JSON.stringify(data));
            }
            else {
                console.error(`[bird][debug][bookmarks] ${message}`);
            }
        }
        async getBookmarksQueryIds() {
            const primary = await this.getQueryId('Bookmarks');
            return Array.from(new Set([primary, 'RV1g3b8n_SGOHwkqKYSCFw', 'tmd4ifV8RHltzn8ymGg1aw']));
        }
        async getBookmarkFolderQueryIds() {
            const primary = await this.getQueryId('BookmarkFolderTimeline');
            return Array.from(new Set([primary, 'KJIQpsvxrTfRIlbaRIySHQ']));
        }
        async getLikesQueryIds() {
            const primary = await this.getQueryId('Likes');
            return Array.from(new Set([primary, 'JR2gceKucIKcVNB_9JkhsA']));
        }
        /**
         * Get the authenticated user's bookmarks
         */
        async getBookmarks(count = 20, options = {}) {
            return this.getBookmarksPaged(count, options);
        }
        async getAllBookmarks(options) {
            return this.getBookmarksPaged(Number.POSITIVE_INFINITY, options);
        }
        /**
         * Get the authenticated user's liked tweets
         */
        async getLikes(count = 20, options = {}) {
            return this.getLikesPaged(count, options);
        }
        async getAllLikes(options) {
            return this.getLikesPaged(Number.POSITIVE_INFINITY, options);
        }
        async getLikesPaged(limit, options = {}) {
            const userResult = await this.getCurrentUser();
            if (!userResult.success || !userResult.user) {
                return { success: false, error: userResult.error ?? 'Could not determine current user' };
            }
            const userId = userResult.user.id;
            const features = buildLikesFeatures();
            const pageSize = 20;
            const seen = new Set();
            const tweets = [];
            let cursor = options.cursor;
            let nextCursor;
            let pagesFetched = 0;
            const { includeRaw = false, maxPages } = options;
            const fetchPage = async (pageCount, pageCursor) => {
                let lastError;
                let had404 = false;
                const queryIds = await this.getLikesQueryIds();
                for (const queryId of queryIds) {
                    const variables = {
                        userId,
                        count: pageCount,
                        includePromotedContent: false,
                        withClientEventToken: false,
                        withBirdwatchNotes: false,
                        withVoice: true,
                        ...(pageCursor ? { cursor: pageCursor } : {}),
                    };
                    const params = new URLSearchParams({
                        variables: JSON.stringify(variables),
                        features: JSON.stringify(features),
                    });
                    const url = `${TWITTER_API_BASE}/${queryId}/Likes?${params.toString()}`;
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
                        const instructions = data.data?.user?.result?.timeline?.timeline?.instructions;
                        if (data.errors && data.errors.length > 0) {
                            const message = data.errors.map((e) => e.message).join(', ');
                            if (!instructions) {
                                if (message.includes('Query: Unspecified')) {
                                    lastError = message;
                                    continue;
                                }
                                return { success: false, error: message, had404 };
                            }
                        }
                        const pageTweets = parseTweetsFromInstructions(instructions, { quoteDepth: this.quoteDepth, includeRaw });
                        const extractedCursor = extractCursorFromInstructions(instructions);
                        return { success: true, tweets: pageTweets, cursor: extractedCursor, had404 };
                    }
                    catch (error) {
                        lastError = error instanceof Error ? error.message : String(error);
                    }
                }
                return { success: false, error: lastError ?? 'Unknown error fetching likes', had404 };
            };
            const fetchWithRefresh = async (pageCount, pageCursor) => {
                const firstAttempt = await fetchPage(pageCount, pageCursor);
                if (firstAttempt.success) {
                    return firstAttempt;
                }
                const shouldRefresh = firstAttempt.had404 ||
                    (typeof firstAttempt.error === 'string' && firstAttempt.error.includes('Query: Unspecified'));
                if (shouldRefresh) {
                    await this.refreshQueryIds();
                    const secondAttempt = await fetchPage(pageCount, pageCursor);
                    if (secondAttempt.success) {
                        return secondAttempt;
                    }
                    return { success: false, error: secondAttempt.error };
                }
                return { success: false, error: firstAttempt.error };
            };
            const unlimited = limit === Number.POSITIVE_INFINITY;
            while (unlimited || tweets.length < limit) {
                const pageCount = unlimited ? pageSize : Math.min(pageSize, limit - tweets.length);
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
                    if (!unlimited && tweets.length >= limit) {
                        break;
                    }
                }
                const pageCursor = page.cursor;
                if (!pageCursor || pageCursor === cursor || page.tweets.length === 0 || added === 0) {
                    nextCursor = undefined;
                    break;
                }
                if (maxPages && pagesFetched >= maxPages) {
                    nextCursor = pageCursor;
                    break;
                }
                cursor = pageCursor;
                nextCursor = pageCursor;
            }
            return { success: true, tweets, nextCursor };
        }
        /**
         * Get the authenticated user's bookmark folder timeline
         */
        async getBookmarkFolderTimeline(folderId, count = 20, options = {}) {
            return this.getBookmarkFolderTimelinePaged(folderId, count, options);
        }
        async getAllBookmarkFolderTimeline(folderId, options) {
            return this.getBookmarkFolderTimelinePaged(folderId, Number.POSITIVE_INFINITY, options);
        }
        async getBookmarksPaged(limit, options = {}) {
            const features = buildBookmarksFeatures();
            const pageSize = 20;
            const seen = new Set();
            const tweets = [];
            let cursor = options.cursor;
            let nextCursor;
            let pagesFetched = 0;
            const { includeRaw = false, maxPages } = options;
            const fetchPage = async (pageCount, pageCursor) => {
                let lastError;
                let had404 = false;
                const queryIds = await this.getBookmarksQueryIds();
                const variables = {
                    count: pageCount,
                    includePromotedContent: false,
                    withDownvotePerspective: false,
                    withReactionsMetadata: false,
                    withReactionsPerspective: false,
                    ...(pageCursor ? { cursor: pageCursor } : {}),
                };
                const params = new URLSearchParams({
                    variables: JSON.stringify(variables),
                    features: JSON.stringify(features),
                });
                for (const queryId of queryIds) {
                    const url = `${TWITTER_API_BASE}/${queryId}/Bookmarks?${params.toString()}`;
                    try {
                        this.logBookmarksDebug('request bookmarks page', {
                            queryId,
                            pageCount,
                            hasCursor: Boolean(pageCursor),
                        });
                        const response = await this.fetchWithRetry(url, {
                            method: 'GET',
                            headers: this.getHeaders(),
                        });
                        if (response.status === 404) {
                            had404 = true;
                            lastError = `HTTP ${response.status}`;
                            this.logBookmarksDebug('bookmarks 404', { queryId });
                            continue;
                        }
                        if (!response.ok) {
                            const text = await response.text();
                            this.logBookmarksDebug('bookmarks non-200', {
                                queryId,
                                status: response.status,
                                body: text.slice(0, 200),
                            });
                            return { success: false, error: `HTTP ${response.status}: ${text.slice(0, 200)}`, had404 };
                        }
                        const data = (await response.json());
                        const instructions = data.data?.bookmark_timeline_v2?.timeline?.instructions;
                        const pageTweets = parseTweetsFromInstructions(instructions, { quoteDepth: this.quoteDepth, includeRaw });
                        const nextCursor = extractCursorFromInstructions(instructions);
                        if (data.errors && data.errors.length > 0) {
                            this.logBookmarksDebug('bookmarks graphql errors (non-fatal)', { queryId, errors: data.errors });
                            if (!instructions) {
                                lastError = data.errors.map((e) => e.message).join(', ');
                                continue;
                            }
                        }
                        this.logBookmarksDebug('bookmarks page parsed', {
                            queryId,
                            tweets: pageTweets.length,
                            hasNextCursor: Boolean(nextCursor),
                        });
                        return { success: true, tweets: pageTweets, cursor: nextCursor, had404 };
                    }
                    catch (error) {
                        lastError = error instanceof Error ? error.message : String(error);
                        this.logBookmarksDebug('bookmarks request error', { queryId, error: lastError });
                    }
                }
                return { success: false, error: lastError ?? 'Unknown error fetching bookmarks', had404 };
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
            const unlimited = limit === Number.POSITIVE_INFINITY;
            while (unlimited || tweets.length < limit) {
                const pageCount = unlimited ? pageSize : Math.min(pageSize, limit - tweets.length);
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
                    if (!unlimited && tweets.length >= limit) {
                        break;
                    }
                }
                const pageCursor = page.cursor;
                if (!pageCursor || pageCursor === cursor || page.tweets.length === 0 || added === 0) {
                    nextCursor = undefined;
                    break;
                }
                if (maxPages && pagesFetched >= maxPages) {
                    nextCursor = pageCursor;
                    break;
                }
                cursor = pageCursor;
                nextCursor = pageCursor;
            }
            return { success: true, tweets, nextCursor };
        }
        async getBookmarkFolderTimelinePaged(folderId, limit, options = {}) {
            const features = buildBookmarksFeatures();
            const pageSize = 20;
            const seen = new Set();
            const tweets = [];
            let cursor = options.cursor;
            let nextCursor;
            let pagesFetched = 0;
            const { includeRaw = false, maxPages } = options;
            const buildVariables = (pageCount, pageCursor, includeCount) => ({
                bookmark_collection_id: folderId,
                includePromotedContent: true,
                ...(includeCount ? { count: pageCount } : {}),
                ...(pageCursor ? { cursor: pageCursor } : {}),
            });
            const fetchPage = async (pageCount, pageCursor) => {
                let lastError;
                let had404 = false;
                const queryIds = await this.getBookmarkFolderQueryIds();
                const tryOnce = async (variables) => {
                    const params = new URLSearchParams({
                        variables: JSON.stringify(variables),
                        features: JSON.stringify(features),
                    });
                    for (const queryId of queryIds) {
                        const url = `${TWITTER_API_BASE}/${queryId}/BookmarkFolderTimeline?${params.toString()}`;
                        try {
                            this.logBookmarksDebug('request bookmark folder page', {
                                queryId,
                                pageCount,
                                hasCursor: Boolean(pageCursor),
                                includeCount: Object.hasOwn(variables, 'count'),
                            });
                            const response = await this.fetchWithRetry(url, {
                                method: 'GET',
                                headers: this.getHeaders(),
                            });
                            if (response.status === 404) {
                                had404 = true;
                                lastError = `HTTP ${response.status}`;
                                this.logBookmarksDebug('bookmark folder 404', { queryId });
                                continue;
                            }
                            if (!response.ok) {
                                const text = await response.text();
                                this.logBookmarksDebug('bookmark folder non-200', {
                                    queryId,
                                    status: response.status,
                                    body: text.slice(0, 200),
                                });
                                return { success: false, error: `HTTP ${response.status}: ${text.slice(0, 200)}`, had404 };
                            }
                            const data = (await response.json());
                            const instructions = data.data?.bookmark_collection_timeline?.timeline?.instructions;
                            const pageTweets = parseTweetsFromInstructions(instructions, { quoteDepth: this.quoteDepth, includeRaw });
                            const nextCursor = extractCursorFromInstructions(instructions);
                            if (data.errors && data.errors.length > 0) {
                                this.logBookmarksDebug('bookmark folder graphql errors (non-fatal)', { queryId, errors: data.errors });
                                if (!instructions) {
                                    lastError = data.errors.map((e) => e.message).join(', ');
                                    continue;
                                }
                            }
                            this.logBookmarksDebug('bookmark folder page parsed', {
                                queryId,
                                tweets: pageTweets.length,
                                hasNextCursor: Boolean(nextCursor),
                            });
                            return { success: true, tweets: pageTweets, cursor: nextCursor, had404 };
                        }
                        catch (error) {
                            lastError = error instanceof Error ? error.message : String(error);
                            this.logBookmarksDebug('bookmark folder request error', { queryId, error: lastError });
                        }
                    }
                    return { success: false, error: lastError ?? 'Unknown error fetching bookmark folder', had404 };
                };
                let attempt = await tryOnce(buildVariables(pageCount, pageCursor, true));
                if (!attempt.success && attempt.error?.includes('Variable "$count"')) {
                    attempt = await tryOnce(buildVariables(pageCount, pageCursor, false));
                }
                if (!attempt.success && attempt.error?.includes('Variable "$cursor"') && pageCursor) {
                    return {
                        success: false,
                        error: 'Bookmark folder pagination rejected the cursor parameter',
                        had404: attempt.had404,
                    };
                }
                return attempt;
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
            const unlimited = limit === Number.POSITIVE_INFINITY;
            while (unlimited || tweets.length < limit) {
                const pageCount = unlimited ? pageSize : Math.min(pageSize, limit - tweets.length);
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
                    if (!unlimited && tweets.length >= limit) {
                        break;
                    }
                }
                const pageCursor = page.cursor;
                if (!pageCursor || pageCursor === cursor || page.tweets.length === 0 || added === 0) {
                    nextCursor = undefined;
                    break;
                }
                if (maxPages && pagesFetched >= maxPages) {
                    nextCursor = pageCursor;
                    break;
                }
                cursor = pageCursor;
                nextCursor = pageCursor;
            }
            return { success: true, tweets, nextCursor };
        }
        async fetchWithRetry(url, init) {
            const maxRetries = 2;
            const baseDelayMs = 500;
            const retryable = new Set([429, 500, 502, 503, 504]);
            for (let attempt = 0; attempt <= maxRetries; attempt += 1) {
                const response = await this.fetchWithTimeout(url, init);
                if (!retryable.has(response.status) || attempt === maxRetries) {
                    return response;
                }
                this.logBookmarksDebug('retrying bookmarks request', {
                    status: response.status,
                    attempt,
                });
                // Retry-After supports delta-seconds only; HTTP-date falls back to backoff.
                const retryAfter = response.headers?.get?.('retry-after');
                const retryAfterMs = retryAfter ? Number.parseInt(retryAfter, 10) * 1000 : Number.NaN;
                const backoffMs = Number.isFinite(retryAfterMs)
                    ? retryAfterMs
                    : baseDelayMs * 2 ** attempt + Math.floor(Math.random() * baseDelayMs);
                await new Promise((resolve) => setTimeout(resolve, backoffMs));
            }
            return this.fetchWithTimeout(url, init);
        }
    }
    return TwitterClientTimelines;
}
//# sourceMappingURL=twitter-client-timelines.js.map