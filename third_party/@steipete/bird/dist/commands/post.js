import { formatTweetUrlLine } from '../lib/output.js';
import { TwitterClient } from '../lib/twitter-client.js';
async function uploadMediaOrExit(client, media, ctx) {
    if (media.length === 0) {
        return undefined;
    }
    const uploaded = [];
    for (const item of media) {
        const res = await client.uploadMedia({ data: item.buffer, mimeType: item.mime, alt: item.alt });
        if (!res.success || !res.mediaId) {
            console.error(`${ctx.p('err')}Media upload failed: ${res.error ?? 'Unknown error'}`);
            process.exit(1);
        }
        uploaded.push(res.mediaId);
    }
    return uploaded;
}
export function registerPostCommands(program, ctx) {
    program
        .command('tweet')
        .description('Post a new tweet')
        .argument('<text>', 'Tweet text')
        .action(async (text) => {
        const opts = program.opts();
        const timeoutMs = ctx.resolveTimeoutFromOptions(opts);
        const quoteDepth = ctx.resolveQuoteDepthFromOptions(opts);
        let media = [];
        try {
            media = ctx.loadMedia({ media: opts.media ?? [], alts: opts.alt ?? [] });
        }
        catch (error) {
            console.error(`${ctx.p('err')}${error instanceof Error ? error.message : String(error)}`);
            process.exit(1);
        }
        const { cookies, warnings } = await ctx.resolveCredentialsFromOptions(opts);
        for (const warning of warnings) {
            console.error(`${ctx.p('warn')}${warning}`);
        }
        if (!cookies.authToken || !cookies.ct0) {
            console.error(`${ctx.p('err')}Missing required credentials`);
            process.exit(1);
        }
        if (cookies.source) {
            console.error(`${ctx.l('source')}${cookies.source}`);
        }
        const client = new TwitterClient({ cookies, timeoutMs, quoteDepth });
        const mediaIds = await uploadMediaOrExit(client, media, ctx);
        const result = await client.tweet(text, mediaIds);
        if (result.success) {
            console.log(`${ctx.p('ok')}Tweet posted successfully!`);
            console.log(formatTweetUrlLine(result.tweetId, ctx.getOutput()));
        }
        else {
            console.error(`${ctx.p('err')}Failed to post tweet: ${result.error}`);
            process.exit(1);
        }
    });
    program
        .command('reply')
        .description('Reply to an existing tweet')
        .argument('<tweet-id-or-url>', 'Tweet ID or URL to reply to')
        .argument('<text>', 'Reply text')
        .action(async (tweetIdOrUrl, text) => {
        const opts = program.opts();
        const timeoutMs = ctx.resolveTimeoutFromOptions(opts);
        const quoteDepth = ctx.resolveQuoteDepthFromOptions(opts);
        let media = [];
        try {
            media = ctx.loadMedia({ media: opts.media ?? [], alts: opts.alt ?? [] });
        }
        catch (error) {
            console.error(`${ctx.p('err')}${error instanceof Error ? error.message : String(error)}`);
            process.exit(1);
        }
        const tweetId = ctx.extractTweetId(tweetIdOrUrl);
        const { cookies, warnings } = await ctx.resolveCredentialsFromOptions(opts);
        for (const warning of warnings) {
            console.error(`${ctx.p('warn')}${warning}`);
        }
        if (!cookies.authToken || !cookies.ct0) {
            console.error(`${ctx.p('err')}Missing required credentials`);
            process.exit(1);
        }
        if (cookies.source) {
            console.error(`${ctx.l('source')}${cookies.source}`);
        }
        console.error(`${ctx.p('info')}Replying to tweet: ${tweetId}`);
        const client = new TwitterClient({ cookies, timeoutMs, quoteDepth });
        const mediaIds = await uploadMediaOrExit(client, media, ctx);
        const result = await client.reply(text, tweetId, mediaIds);
        if (result.success) {
            console.log(`${ctx.p('ok')}Reply posted successfully!`);
            console.log(formatTweetUrlLine(result.tweetId, ctx.getOutput()));
        }
        else {
            console.error(`${ctx.p('err')}Failed to post reply: ${result.error}`);
            process.exit(1);
        }
    });
}
//# sourceMappingURL=post.js.map