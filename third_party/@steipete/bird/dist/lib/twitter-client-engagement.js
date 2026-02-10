import { TWITTER_API_BASE, TWITTER_GRAPHQL_POST_URL } from './twitter-client-constants.js';
export function withEngagement(Base) {
    class TwitterClientEngagement extends Base {
        // biome-ignore lint/complexity/noUselessConstructor lint/suspicious/noExplicitAny: TS mixin constructor requirement.
        constructor(...args) {
            super(...args);
        }
        async performEngagementMutation(operationName, tweetId) {
            await this.ensureClientUserId();
            const variables = operationName === 'DeleteRetweet' ? { tweet_id: tweetId, source_tweet_id: tweetId } : { tweet_id: tweetId };
            let queryId = await this.getQueryId(operationName);
            let urlWithOperation = `${TWITTER_API_BASE}/${queryId}/${operationName}`;
            const buildBody = () => JSON.stringify({ variables, queryId });
            const buildHeaders = () => ({ ...this.getHeaders(), referer: `https://x.com/i/status/${tweetId}` });
            let body = buildBody();
            const parseResponse = async (response) => {
                if (!response.ok) {
                    const text = await response.text();
                    return { success: false, error: `HTTP ${response.status}: ${text.slice(0, 200)}` };
                }
                const data = (await response.json());
                if (data.errors && data.errors.length > 0) {
                    return { success: false, error: data.errors.map((e) => e.message).join(', ') };
                }
                return { success: true };
            };
            try {
                let response = await this.fetchWithTimeout(urlWithOperation, {
                    method: 'POST',
                    headers: buildHeaders(),
                    body,
                });
                if (response.status === 404) {
                    await this.refreshQueryIds();
                    queryId = await this.getQueryId(operationName);
                    urlWithOperation = `${TWITTER_API_BASE}/${queryId}/${operationName}`;
                    body = buildBody();
                    response = await this.fetchWithTimeout(urlWithOperation, {
                        method: 'POST',
                        headers: buildHeaders(),
                        body,
                    });
                    if (response.status === 404) {
                        const retry = await this.fetchWithTimeout(TWITTER_GRAPHQL_POST_URL, {
                            method: 'POST',
                            headers: buildHeaders(),
                            body,
                        });
                        return parseResponse(retry);
                    }
                }
                return parseResponse(response);
            }
            catch (error) {
                return { success: false, error: error instanceof Error ? error.message : String(error) };
            }
        }
        /** Like a tweet. */
        async like(tweetId) {
            return this.performEngagementMutation('FavoriteTweet', tweetId);
        }
        /** Remove a like from a tweet. */
        async unlike(tweetId) {
            return this.performEngagementMutation('UnfavoriteTweet', tweetId);
        }
        /** Retweet a tweet. */
        async retweet(tweetId) {
            return this.performEngagementMutation('CreateRetweet', tweetId);
        }
        /** Remove a retweet. */
        async unretweet(tweetId) {
            return this.performEngagementMutation('DeleteRetweet', tweetId);
        }
        /** Bookmark a tweet. */
        async bookmark(tweetId) {
            return this.performEngagementMutation('CreateBookmark', tweetId);
        }
    }
    return TwitterClientEngagement;
}
//# sourceMappingURL=twitter-client-engagement.js.map