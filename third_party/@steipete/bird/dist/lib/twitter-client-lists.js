// ABOUTME: Mixin for Twitter Lists GraphQL operations.
// ABOUTME: Provides methods to fetch user's owned lists, memberships, and list timelines.
import { TWITTER_API_BASE } from './twitter-client-constants.js';
import { buildListsFeatures } from './twitter-client-features.js';
import { extractCursorFromInstructions, parseTweetsFromInstructions } from './twitter-client-utils.js';
function parseList(listResult) {
    if (!listResult.id_str || !listResult.name) {
        return null;
    }
    const owner = listResult.user_results?.result;
    return {
        id: listResult.id_str,
        name: listResult.name,
        description: listResult.description,
        memberCount: listResult.member_count,
        subscriberCount: listResult.subscriber_count,
        isPrivate: listResult.mode?.toLowerCase() === 'private',
        createdAt: listResult.created_at,
        owner: owner
            ? {
                id: owner.rest_id ?? '',
                username: owner.legacy?.screen_name ?? '',
                name: owner.legacy?.name ?? '',
            }
            : undefined,
    };
}
function parseListsFromInstructions(instructions) {
    const lists = [];
    if (!instructions) {
        return lists;
    }
    for (const instruction of instructions) {
        if (!instruction.entries) {
            continue;
        }
        for (const entry of instruction.entries) {
            const listResult = entry.content?.itemContent?.list;
            if (listResult) {
                const parsed = parseList(listResult);
                if (parsed) {
                    lists.push(parsed);
                }
            }
        }
    }
    return lists;
}
export function withLists(Base) {
    class TwitterClientLists extends Base {
        // biome-ignore lint/complexity/noUselessConstructor lint/suspicious/noExplicitAny: TS mixin constructor requirement.
        constructor(...args) {
            super(...args);
        }
        async getListOwnershipsQueryIds() {
            const primary = await this.getQueryId('ListOwnerships');
            return Array.from(new Set([primary, 'wQcOSjSQ8NtgxIwvYl1lMg']));
        }
        async getListMembershipsQueryIds() {
            const primary = await this.getQueryId('ListMemberships');
            return Array.from(new Set([primary, 'BlEXXdARdSeL_0KyKHHvvg']));
        }
        async getListTimelineQueryIds() {
            const primary = await this.getQueryId('ListLatestTweetsTimeline');
            return Array.from(new Set([primary, '2TemLyqrMpTeAmysdbnVqw']));
        }
        /**
         * Get lists owned by the authenticated user
         */
        async getOwnedLists(count = 100) {
            const userResult = await this.getCurrentUser();
            if (!userResult.success || !userResult.user) {
                return { success: false, error: userResult.error ?? 'Could not determine current user' };
            }
            const variables = {
                userId: userResult.user.id,
                count,
                isListMembershipShown: true,
                isListMemberTargetUserId: userResult.user.id,
            };
            const features = buildListsFeatures();
            const params = new URLSearchParams({
                variables: JSON.stringify(variables),
                features: JSON.stringify(features),
            });
            const tryOnce = async () => {
                let lastError;
                let had404 = false;
                const queryIds = await this.getListOwnershipsQueryIds();
                for (const queryId of queryIds) {
                    const url = `${TWITTER_API_BASE}/${queryId}/ListOwnerships?${params.toString()}`;
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
                        const instructions = data.data?.user?.result?.timeline?.timeline?.instructions;
                        const lists = parseListsFromInstructions(instructions);
                        return { success: true, lists, had404 };
                    }
                    catch (error) {
                        lastError = error instanceof Error ? error.message : String(error);
                    }
                }
                return { success: false, error: lastError ?? 'Unknown error fetching owned lists', had404 };
            };
            const firstAttempt = await tryOnce();
            if (firstAttempt.success) {
                return { success: true, lists: firstAttempt.lists };
            }
            if (firstAttempt.had404) {
                await this.refreshQueryIds();
                const secondAttempt = await tryOnce();
                if (secondAttempt.success) {
                    return { success: true, lists: secondAttempt.lists };
                }
                return { success: false, error: secondAttempt.error };
            }
            return { success: false, error: firstAttempt.error };
        }
        /**
         * Get lists the authenticated user is a member of
         */
        async getListMemberships(count = 100) {
            const userResult = await this.getCurrentUser();
            if (!userResult.success || !userResult.user) {
                return { success: false, error: userResult.error ?? 'Could not determine current user' };
            }
            const variables = {
                userId: userResult.user.id,
                count,
                isListMembershipShown: true,
                isListMemberTargetUserId: userResult.user.id,
            };
            const features = buildListsFeatures();
            const params = new URLSearchParams({
                variables: JSON.stringify(variables),
                features: JSON.stringify(features),
            });
            const tryOnce = async () => {
                let lastError;
                let had404 = false;
                const queryIds = await this.getListMembershipsQueryIds();
                for (const queryId of queryIds) {
                    const url = `${TWITTER_API_BASE}/${queryId}/ListMemberships?${params.toString()}`;
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
                        const instructions = data.data?.user?.result?.timeline?.timeline?.instructions;
                        const lists = parseListsFromInstructions(instructions);
                        return { success: true, lists, had404 };
                    }
                    catch (error) {
                        lastError = error instanceof Error ? error.message : String(error);
                    }
                }
                return { success: false, error: lastError ?? 'Unknown error fetching list memberships', had404 };
            };
            const firstAttempt = await tryOnce();
            if (firstAttempt.success) {
                return { success: true, lists: firstAttempt.lists };
            }
            if (firstAttempt.had404) {
                await this.refreshQueryIds();
                const secondAttempt = await tryOnce();
                if (secondAttempt.success) {
                    return { success: true, lists: secondAttempt.lists };
                }
                return { success: false, error: secondAttempt.error };
            }
            return { success: false, error: firstAttempt.error };
        }
        /**
         * Get tweets from a list timeline
         */
        async getListTimeline(listId, count = 20, options = {}) {
            return this.getListTimelinePaged(listId, count, options);
        }
        /**
         * Get all tweets from a list timeline (paginated)
         */
        async getAllListTimeline(listId, options) {
            return this.getListTimelinePaged(listId, Number.POSITIVE_INFINITY, options);
        }
        /**
         * Internal paginated list timeline fetcher
         */
        async getListTimelinePaged(listId, limit, options = {}) {
            const features = buildListsFeatures();
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
                const queryIds = await this.getListTimelineQueryIds();
                const variables = {
                    listId,
                    count: pageCount,
                    ...(pageCursor ? { cursor: pageCursor } : {}),
                };
                const params = new URLSearchParams({
                    variables: JSON.stringify(variables),
                    features: JSON.stringify(features),
                });
                for (const queryId of queryIds) {
                    const url = `${TWITTER_API_BASE}/${queryId}/ListLatestTweetsTimeline?${params.toString()}`;
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
                        const instructions = data.data?.list?.tweets_timeline?.timeline?.instructions;
                        const pageTweets = parseTweetsFromInstructions(instructions, { quoteDepth: this.quoteDepth, includeRaw });
                        const nextCursor = extractCursorFromInstructions(instructions);
                        return { success: true, tweets: pageTweets, cursor: nextCursor, had404 };
                    }
                    catch (error) {
                        lastError = error instanceof Error ? error.message : String(error);
                    }
                }
                return { success: false, error: lastError ?? 'Unknown error fetching list timeline', had404 };
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
    }
    return TwitterClientLists;
}
//# sourceMappingURL=twitter-client-lists.js.map