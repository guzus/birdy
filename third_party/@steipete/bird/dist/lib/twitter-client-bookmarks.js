import { TWITTER_API_BASE, TWITTER_GRAPHQL_POST_URL } from './twitter-client-constants.js';
export function withBookmarks(Base) {
    class TwitterClientBookmarks extends Base {
        // biome-ignore lint/complexity/noUselessConstructor lint/suspicious/noExplicitAny: TS mixin constructor requirement.
        constructor(...args) {
            super(...args);
        }
        async unbookmark(tweetId) {
            // TODO: verify if DeleteBookmark requires client user ID or additional payload fields; add ensureClientUserId() if needed (needs live API test).
            const variables = { tweet_id: tweetId };
            let queryId = await this.getQueryId('DeleteBookmark');
            let urlWithOperation = `${TWITTER_API_BASE}/${queryId}/DeleteBookmark`;
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
                    queryId = await this.getQueryId('DeleteBookmark');
                    urlWithOperation = `${TWITTER_API_BASE}/${queryId}/DeleteBookmark`;
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
    }
    return TwitterClientBookmarks;
}
//# sourceMappingURL=twitter-client-bookmarks.js.map