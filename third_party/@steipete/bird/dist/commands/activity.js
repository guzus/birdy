import { parsePaginationFlags } from '../cli/pagination.js';
import { TwitterClient } from '../lib/twitter-client.js';
function normalizeActivityTypes(raw) {
    const value = raw || 'likes,reposts,quotes';
    const aliases = {
        like: 'likes',
        likes: 'likes',
        liker: 'likes',
        likers: 'likes',
        repost: 'reposts',
        reposts: 'reposts',
        retweet: 'reposts',
        retweets: 'reposts',
        quote: 'quotes',
        quotes: 'quotes',
    };
    const types = [];
    for (const token of value.split(',')) {
        const normalized = aliases[token.trim().toLowerCase()];
        if (!normalized) {
            return { ok: false, error: `Invalid --types value "${token}". Use likes,reposts,quotes.` };
        }
        if (!types.includes(normalized)) {
            types.push(normalized);
        }
    }
    if (types.length === 0) {
        return { ok: false, error: 'At least one activity type is required.' };
    }
    return { ok: true, types };
}
function printUsers(users, ctx, label) {
    console.log(`\n${label} (${users.length})`);
    if (users.length === 0) {
        console.log('No users found.');
        return;
    }
    for (const user of users) {
        console.log(`@${user.username} (${user.name})`);
        if (user.description) {
            console.log(`  ${user.description.slice(0, 100)}${user.description.length > 100 ? '...' : ''}`);
        }
        if (user.followersCount !== undefined) {
            console.log(`  ${ctx.p('info')}${user.followersCount.toLocaleString()} followers`);
        }
        console.log(`  ${ctx.l('url')}https://x.com/${user.username}`);
    }
}
function printActivityResult(result, ctx, selectedTypes) {
    if (selectedTypes.includes('likes')) {
        printUsers(result.likes.users, ctx, 'Likes');
        if (result.likes.nextCursor) {
            console.error(`${ctx.p('info')}More likes available. Use --types likes --cursor "${result.likes.nextCursor}" to continue.`);
        }
    }
    if (selectedTypes.includes('reposts')) {
        printUsers(result.reposts.users, ctx, 'Reposts');
        if (result.reposts.nextCursor) {
            console.error(`${ctx.p('info')}More reposts available. Use --types reposts --cursor "${result.reposts.nextCursor}" to continue.`);
        }
    }
    if (selectedTypes.includes('quotes')) {
        console.log(`\nQuotes (${result.quotes.tweets.length})`);
        ctx.printTweets(result.quotes.tweets, { emptyMessage: 'No quote posts found.' });
        if (result.quotes.nextCursor) {
            console.error(`${ctx.p('info')}More quotes available. Use --types quotes --cursor "${result.quotes.nextCursor}" to continue.`);
        }
    }
}
export function registerActivityCommand(program, ctx) {
    program
        .command('activity')
        .description('Get users and quote posts from a tweet activity page')
        .argument('<tweet-id-or-url>', 'Tweet ID or URL')
        .option('--types <types>', 'Comma-separated activity types: likes,reposts,quotes', 'likes,reposts,quotes')
        .option('-n, --count <number>', 'Number of rows to fetch per selected type', '20')
        .option('--all', 'Fetch all rows for selected types (paged)')
        .option('--max-pages <number>', 'Stop after N pages when paginating')
        .option('--delay <ms>', 'Delay in ms between page fetches', '1000')
        .option('--cursor <string>', 'Resume pagination for a single selected type')
        .option('--json', 'Output as JSON')
        .option('--json-full', 'Output as JSON with full raw tweet response in quote posts')
        .action(async (tweetIdOrUrl, cmdOpts) => {
        const typesResult = normalizeActivityTypes(cmdOpts.types);
        if (!typesResult.ok) {
            console.error(`${ctx.p('err')}${typesResult.error}`);
            process.exit(1);
        }
        const selectedTypes = typesResult.types;
        const pagination = parsePaginationFlags(cmdOpts, {
            maxPagesImpliesPagination: true,
            includeDelay: true,
        });
        if (!pagination.ok) {
            console.error(`${ctx.p('err')}${pagination.error}`);
            process.exit(1);
        }
        if (pagination.cursor && selectedTypes.length !== 1) {
            console.error(`${ctx.p('err')}--cursor can only be used with a single --types value.`);
            process.exit(1);
        }
        const count = Number.parseInt(cmdOpts.count || '20', 10);
        if (!pagination.usePagination && (!Number.isFinite(count) || count <= 0)) {
            console.error(`${ctx.p('err')}Invalid --count. Expected a positive integer.`);
            process.exit(1);
        }
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
        const pageOptions = {
            maxPages: pagination.maxPages,
            cursor: pagination.cursor,
            pageDelayMs: pagination.pageDelayMs,
        };
        const quoteOptions = { ...pageOptions, includeRaw };
        const result = {
            tweetId,
            likes: { users: [], nextCursor: null },
            reposts: { users: [], nextCursor: null },
            quotes: { tweets: [], nextCursor: null },
        };
        const run = [];
        if (selectedTypes.includes('likes')) {
            run.push((async () => {
                const page = pagination.usePagination
                    ? await client.getAllFavoriters(tweetId, pageOptions)
                    : await client.getFavoriters(tweetId, count);
                if (!page.success || !page.users) {
                    throw new Error(`Failed to fetch likes: ${page.error}`);
                }
                result.likes = { users: page.users, nextCursor: page.nextCursor ?? null };
            })());
        }
        if (selectedTypes.includes('reposts')) {
            run.push((async () => {
                const page = pagination.usePagination
                    ? await client.getAllRetweeters(tweetId, pageOptions)
                    : await client.getRetweeters(tweetId, count);
                if (!page.success || !page.users) {
                    throw new Error(`Failed to fetch reposts: ${page.error}`);
                }
                result.reposts = { users: page.users, nextCursor: page.nextCursor ?? null };
            })());
        }
        if (selectedTypes.includes('quotes')) {
            run.push((async () => {
                const page = pagination.usePagination
                    ? await client.getAllQuoteTweets(tweetId, quoteOptions)
                    : await client.getQuoteTweets(tweetId, count, { includeRaw });
                if (!page.success || !page.tweets) {
                    throw new Error(`Failed to fetch quotes: ${page.error}`);
                }
                result.quotes = { tweets: page.tweets, nextCursor: page.nextCursor ?? null };
            })());
        }
        try {
            await Promise.all(run);
        }
        catch (error) {
            console.error(`${ctx.p('err')}${error instanceof Error ? error.message : String(error)}`);
            process.exit(1);
        }
        if (cmdOpts.json || cmdOpts.jsonFull) {
            console.log(JSON.stringify(result, null, 2));
            return;
        }
        printActivityResult(result, ctx, selectedTypes);
    });
}
//# sourceMappingURL=activity.js.map
