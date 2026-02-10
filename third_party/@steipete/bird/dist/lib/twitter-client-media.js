import { TWITTER_MEDIA_METADATA_URL, TWITTER_UPLOAD_URL } from './twitter-client-constants.js';
export function withMedia(Base) {
    class TwitterClientMedia extends Base {
        // biome-ignore lint/complexity/noUselessConstructor lint/suspicious/noExplicitAny: TS mixin constructor requirement.
        constructor(...args) {
            super(...args);
        }
        mediaCategoryForMime(mimeType) {
            if (mimeType.startsWith('image/')) {
                if (mimeType === 'image/gif') {
                    return 'tweet_gif';
                }
                return 'tweet_image';
            }
            if (mimeType.startsWith('video/')) {
                return 'tweet_video';
            }
            return null;
        }
        async uploadMedia(input) {
            const category = this.mediaCategoryForMime(input.mimeType);
            if (!category) {
                return { success: false, error: `Unsupported media type: ${input.mimeType}` };
            }
            try {
                const initParams = new URLSearchParams({
                    command: 'INIT',
                    total_bytes: String(input.data.byteLength),
                    media_type: input.mimeType,
                    media_category: category,
                });
                const initResp = await this.fetchWithTimeout(TWITTER_UPLOAD_URL, {
                    method: 'POST',
                    headers: this.getUploadHeaders(),
                    body: initParams,
                });
                if (!initResp.ok) {
                    const text = await initResp.text();
                    return { success: false, error: `HTTP ${initResp.status}: ${text.slice(0, 200)}` };
                }
                const initBody = (await initResp.json());
                const mediaId = typeof initBody.media_id_string === 'string'
                    ? initBody.media_id_string
                    : initBody.media_id !== undefined
                        ? String(initBody.media_id)
                        : undefined;
                if (!mediaId) {
                    return { success: false, error: 'Media upload INIT did not return media_id' };
                }
                const chunkSize = 5 * 1024 * 1024;
                let segmentIndex = 0;
                for (let offset = 0; offset < input.data.byteLength; offset += chunkSize) {
                    const chunk = input.data.slice(offset, Math.min(input.data.byteLength, offset + chunkSize));
                    const form = new FormData();
                    form.set('command', 'APPEND');
                    form.set('media_id', mediaId);
                    form.set('segment_index', String(segmentIndex));
                    form.set('media', new Blob([chunk], { type: input.mimeType }), 'media');
                    const appendResp = await this.fetchWithTimeout(TWITTER_UPLOAD_URL, {
                        method: 'POST',
                        headers: this.getUploadHeaders(),
                        body: form,
                    });
                    if (!appendResp.ok) {
                        const text = await appendResp.text();
                        return { success: false, error: `HTTP ${appendResp.status}: ${text.slice(0, 200)}` };
                    }
                    segmentIndex += 1;
                }
                const finalizeParams = new URLSearchParams({ command: 'FINALIZE', media_id: mediaId });
                const finalizeResp = await this.fetchWithTimeout(TWITTER_UPLOAD_URL, {
                    method: 'POST',
                    headers: this.getUploadHeaders(),
                    body: finalizeParams,
                });
                if (!finalizeResp.ok) {
                    const text = await finalizeResp.text();
                    return { success: false, error: `HTTP ${finalizeResp.status}: ${text.slice(0, 200)}` };
                }
                const finalizeBody = (await finalizeResp.json());
                const info = finalizeBody.processing_info;
                if (info?.state && info.state !== 'succeeded') {
                    let attempts = 0;
                    while (attempts < 20) {
                        if (info.state === 'failed') {
                            const msg = info.error?.message || info.error?.name || 'Media processing failed';
                            return { success: false, error: msg };
                        }
                        const delaySecs = Number.isFinite(info.check_after_secs) ? Math.max(1, info.check_after_secs) : 2;
                        await this.sleep(delaySecs * 1000);
                        const statusUrl = `${TWITTER_UPLOAD_URL}?${new URLSearchParams({
                            command: 'STATUS',
                            media_id: mediaId,
                        }).toString()}`;
                        const statusResp = await this.fetchWithTimeout(statusUrl, {
                            method: 'GET',
                            headers: this.getUploadHeaders(),
                        });
                        if (!statusResp.ok) {
                            const text = await statusResp.text();
                            return { success: false, error: `HTTP ${statusResp.status}: ${text.slice(0, 200)}` };
                        }
                        const statusBody = (await statusResp.json());
                        if (!statusBody.processing_info) {
                            break;
                        }
                        info.state = statusBody.processing_info.state;
                        info.check_after_secs = statusBody.processing_info.check_after_secs;
                        info.error = statusBody.processing_info.error;
                        if (info.state === 'succeeded') {
                            break;
                        }
                        attempts += 1;
                    }
                }
                if (input.alt && input.mimeType.startsWith('image/')) {
                    const metaResp = await this.fetchWithTimeout(TWITTER_MEDIA_METADATA_URL, {
                        method: 'POST',
                        headers: this.getJsonHeaders(),
                        body: JSON.stringify({ media_id: mediaId, alt_text: { text: input.alt } }),
                    });
                    if (!metaResp.ok) {
                        const text = await metaResp.text();
                        return { success: false, error: `HTTP ${metaResp.status}: ${text.slice(0, 200)}` };
                    }
                }
                return { success: true, mediaId };
            }
            catch (error) {
                return { success: false, error: error instanceof Error ? error.message : String(error) };
            }
        }
    }
    return TwitterClientMedia;
}
//# sourceMappingURL=twitter-client-media.js.map