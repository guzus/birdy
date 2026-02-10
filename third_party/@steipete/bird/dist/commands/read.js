import { parsePaginationFlags } from '../cli/pagination.js';
import { formatStatsLine } from '../lib/output.js';
import { TwitterClient } from '../lib/twitter-client.js';
export function registerReadCommands(program, ctx) {
    program
        .command('read')
        .description('Read/fetch a tweet by ID or URL')
        .argument('<tweet-id-or-url>', 'Tweet ID or URL to read')
        .option('--json', 'Output as JSON')
        .option('--json-full', 'Output as JSON with full raw API response in _raw field')
        .action(async (tweetIdOrUrl, cmdOpts) => {
        const opts = program.opts();
        const timeoutMs = ctx.resolveTimeoutFromOptions(opts);
        const quoteDepth = ctx.resolveQuoteDepthFromOptions(opts);
        const tweetId = ctx.extractTweetId(tweetIdOrUrl);
        const { cookies, warnings } = await ctx.resolveCredentialsFromOptions(opts);
        for (const warning of warnings) {
            console.error(`${ctx.p('warn')}${warning}`);
        }
        if (!cookies.authToken || !cookies.ct0) {
            console.error(`${ctx.p('err')}Missing required credentials`);
            process.exit(1);
        }
        const client = new TwitterClient({ cookies, timeoutMs, quoteDepth });
        const includeRaw = cmdOpts.jsonFull ?? false;
        const result = await client.getTweet(tweetId, { includeRaw });
        if (result.success && result.tweet) {
            if (cmdOpts.json || cmdOpts.jsonFull) {
                console.log(JSON.stringify(result.tweet, null, 2));
            }
            else {
                ctx.printTweets([result.tweet], { showSeparator: false });
                console.log(formatStatsLine(result.tweet, ctx.getOutput()));
            }
        }
        else {
            console.error(`${ctx.p('err')}Failed to read tweet: ${result.error}`);
            process.exit(1);
        }
    });
    program
        .command('replies')
        .description('List replies to a tweet (by ID or URL)')
        .argument('<tweet-id-or-url>', 'Tweet ID or URL')
        .option('--all', 'Fetch all replies (paged)')
        .option('--max-pages <number>', 'Fetch N pages (implies pagination)')
        .option('--delay <ms>', 'Delay in ms between page fetches', '1000')
        .option('--cursor <string>', 'Resume pagination from a cursor')
        .option('--json', 'Output as JSON')
        .option('--json-full', 'Output as JSON with full raw API response in _raw field')
        .action(async (tweetIdOrUrl, cmdOpts) => {
        const opts = program.opts();
        const timeoutMs = ctx.resolveTimeoutFromOptions(opts);
        const quoteDepth = ctx.resolveQuoteDepthFromOptions(opts);
        const tweetId = ctx.extractTweetId(tweetIdOrUrl);
        const pagination = parsePaginationFlags(cmdOpts, { maxPagesImpliesPagination: true, includeDelay: true });
        if (!pagination.ok) {
            console.error(`${ctx.p('err')}${pagination.error}`);
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
        const client = new TwitterClient({ cookies, timeoutMs, quoteDepth });
        const includeRaw = cmdOpts.jsonFull ?? false;
        const result = pagination.usePagination
            ? await client.getRepliesPaged(tweetId, {
                includeRaw,
                maxPages: pagination.maxPages,
                cursor: pagination.cursor,
                pageDelayMs: pagination.pageDelayMs,
            })
            : await client.getReplies(tweetId, { includeRaw });
        const isJson = Boolean(cmdOpts.json || cmdOpts.jsonFull);
        if (result.tweets) {
            ctx.printTweetsResult(result, {
                json: isJson,
                usePagination: pagination.usePagination,
                emptyMessage: 'No replies found.',
            });
            // Show pagination hint if there's more
            if (result.nextCursor && !isJson) {
                console.error(`${ctx.p('info')}More replies available. Use --cursor "${result.nextCursor}" to continue.`);
            }
        }
        if (!result.success) {
            console.error(`${ctx.p('err')}Failed to fetch replies: ${result.error}`);
            process.exit(1);
        }
    });
    program
        .command('thread')
        .description('Show the full conversation thread containing the tweet')
        .argument('<tweet-id-or-url>', 'Tweet ID or URL')
        .option('--all', 'Fetch all thread tweets (paged)')
        .option('--max-pages <number>', 'Fetch N pages (implies pagination)')
        .option('--delay <ms>', 'Delay in ms between page fetches', '1000')
        .option('--cursor <string>', 'Resume pagination from a cursor')
        .option('--json', 'Output as JSON')
        .option('--json-full', 'Output as JSON with full raw API response in _raw field')
        .action(async (tweetIdOrUrl, cmdOpts) => {
        const opts = program.opts();
        const timeoutMs = ctx.resolveTimeoutFromOptions(opts);
        const quoteDepth = ctx.resolveQuoteDepthFromOptions(opts);
        const tweetId = ctx.extractTweetId(tweetIdOrUrl);
        const pagination = parsePaginationFlags(cmdOpts, { maxPagesImpliesPagination: true, includeDelay: true });
        if (!pagination.ok) {
            console.error(`${ctx.p('err')}${pagination.error}`);
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
        const client = new TwitterClient({ cookies, timeoutMs, quoteDepth });
        const includeRaw = cmdOpts.jsonFull ?? false;
        const result = pagination.usePagination
            ? await client.getThreadPaged(tweetId, {
                includeRaw,
                maxPages: pagination.maxPages,
                cursor: pagination.cursor,
                pageDelayMs: pagination.pageDelayMs,
            })
            : await client.getThread(tweetId, { includeRaw });
        const isJson = Boolean(cmdOpts.json || cmdOpts.jsonFull);
        if (result.tweets) {
            ctx.printTweetsResult(result, {
                json: isJson,
                usePagination: pagination.usePagination,
                emptyMessage: 'No thread tweets found.',
            });
            // Show pagination hint if there's more
            if (result.nextCursor && !isJson) {
                console.error(`${ctx.p('info')}More thread tweets available. Use --cursor "${result.nextCursor}" to continue.`);
            }
        }
        if (!result.success) {
            console.error(`${ctx.p('err')}Failed to fetch thread: ${result.error}`);
            process.exit(1);
        }
    });
}
//# sourceMappingURL=read.js.map