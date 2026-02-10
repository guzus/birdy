import { existsSync, readFileSync } from 'node:fs';
import { homedir } from 'node:os';
import { join } from 'node:path';
import JSON5 from 'json5';
import kleur from 'kleur';
import { resolveCredentials } from '../lib/cookies.js';
import { extractTweetId } from '../lib/extract-tweet-id.js';
import { hyperlink, labelPrefix, resolveOutputConfigFromArgv, resolveOutputConfigFromCommander, statusPrefix, } from '../lib/output.js';
const COOKIE_SOURCES = ['safari', 'chrome', 'firefox'];
function parseCookieSource(value) {
    const normalized = value.trim().toLowerCase();
    if (normalized === 'safari' || normalized === 'chrome' || normalized === 'firefox') {
        return normalized;
    }
    throw new Error(`Invalid --cookie-source "${value}". Allowed: safari, chrome, firefox.`);
}
export const collectCookieSource = (value, previous = []) => {
    previous.push(parseCookieSource(value));
    return previous;
};
function resolveCookieSourceOrder(input) {
    if (typeof input === 'string') {
        return [parseCookieSource(input)];
    }
    if (Array.isArray(input)) {
        const result = [];
        for (const entry of input) {
            if (typeof entry !== 'string') {
                continue;
            }
            result.push(parseCookieSource(entry));
        }
        return result.length > 0 ? result : undefined;
    }
    return undefined;
}
function resolveTimeoutMs(...values) {
    for (const value of values) {
        if (value === undefined || value === null || value === '') {
            continue;
        }
        const parsed = typeof value === 'number' ? value : Number(value);
        if (Number.isFinite(parsed) && parsed > 0) {
            return parsed;
        }
    }
    return undefined;
}
function resolveQuoteDepth(...values) {
    for (const value of values) {
        if (value === undefined || value === null || value === '') {
            continue;
        }
        const parsed = typeof value === 'number' ? value : Number.parseInt(value, 10);
        if (Number.isFinite(parsed) && parsed >= 0) {
            return Math.floor(parsed);
        }
    }
    return undefined;
}
function detectMime(path) {
    const ext = path.toLowerCase();
    if (ext.endsWith('.jpg') || ext.endsWith('.jpeg')) {
        return 'image/jpeg';
    }
    if (ext.endsWith('.png')) {
        return 'image/png';
    }
    if (ext.endsWith('.webp')) {
        return 'image/webp';
    }
    if (ext.endsWith('.gif')) {
        return 'image/gif';
    }
    if (ext.endsWith('.mp4') || ext.endsWith('.m4v')) {
        return 'video/mp4';
    }
    if (ext.endsWith('.mov')) {
        return 'video/quicktime';
    }
    return null;
}
function readConfigFile(path, warn) {
    if (!existsSync(path)) {
        return {};
    }
    try {
        const raw = readFileSync(path, 'utf8');
        const parsed = JSON5.parse(raw);
        return parsed ?? {};
    }
    catch (error) {
        warn(`Failed to parse config at ${path}: ${error instanceof Error ? error.message : String(error)}`);
        return {};
    }
}
function loadConfig(warn) {
    const globalPath = join(homedir(), '.config', 'bird', 'config.json5');
    const localPath = join(process.cwd(), '.birdrc.json5');
    return {
        ...readConfigFile(globalPath, warn),
        ...readConfigFile(localPath, warn),
    };
}
export function createCliContext(normalizedArgs, env = process.env) {
    const isTty = process.stdout.isTTY;
    let output = resolveOutputConfigFromArgv(normalizedArgs, env, isTty);
    kleur.enabled = output.color;
    const wrap = (styler) => (text) => isTty ? styler(text) : text;
    const colors = {
        banner: wrap((t) => kleur.bold().blue(t)),
        subtitle: wrap((t) => kleur.dim(t)),
        section: wrap((t) => kleur.bold().white(t)),
        bullet: wrap((t) => kleur.blue(t)),
        command: wrap((t) => kleur.bold().cyan(t)),
        option: wrap((t) => kleur.cyan(t)),
        argument: wrap((t) => kleur.magenta(t)),
        description: wrap((t) => kleur.white(t)),
        muted: wrap((t) => kleur.gray(t)),
        accent: wrap((t) => kleur.green(t)),
    };
    const p = (kind) => {
        const prefix = statusPrefix(kind, output);
        if (output.plain || !output.color) {
            return prefix;
        }
        if (kind === 'ok') {
            return kleur.green(prefix);
        }
        if (kind === 'warn') {
            return kleur.yellow(prefix);
        }
        if (kind === 'err') {
            return kleur.red(prefix);
        }
        if (kind === 'info') {
            return kleur.cyan(prefix);
        }
        return kleur.gray(prefix);
    };
    const l = (kind) => {
        const prefix = labelPrefix(kind, output);
        if (output.plain || !output.color) {
            return prefix;
        }
        if (kind === 'url') {
            return kleur.cyan(prefix);
        }
        if (kind === 'date') {
            return kleur.magenta(prefix);
        }
        if (kind === 'source') {
            return kleur.gray(prefix);
        }
        if (kind === 'engine') {
            return kleur.blue(prefix);
        }
        if (kind === 'credentials') {
            return kleur.yellow(prefix);
        }
        if (kind === 'user') {
            return kleur.cyan(prefix);
        }
        if (kind === 'userId') {
            return kleur.magenta(prefix);
        }
        if (kind === 'email') {
            return kleur.green(prefix);
        }
        return kleur.gray(prefix);
    };
    const config = loadConfig((message) => {
        console.error(colors.muted(`${p('warn')}${message}`));
    });
    function applyOutputFromCommand(command) {
        const opts = command.optsWithGlobals();
        output = resolveOutputConfigFromCommander(opts, env, isTty);
        kleur.enabled = output.color;
    }
    function resolveTimeoutFromOptions(options) {
        return resolveTimeoutMs(options.timeout, config.timeoutMs, env.BIRD_TIMEOUT_MS);
    }
    function resolveCookieTimeoutFromOptions(options) {
        return resolveTimeoutMs(options.cookieTimeout, config.cookieTimeoutMs, env.BIRD_COOKIE_TIMEOUT_MS);
    }
    function resolveQuoteDepthFromOptions(options) {
        return resolveQuoteDepth(options.quoteDepth, config.quoteDepth, env.BIRD_QUOTE_DEPTH);
    }
    function resolveCredentialsFromOptions(opts) {
        const cookieSource = opts.cookieSource?.length
            ? opts.cookieSource
            : (resolveCookieSourceOrder(config.cookieSource) ?? COOKIE_SOURCES);
        const chromeProfile = opts.chromeProfileDir || opts.chromeProfile || config.chromeProfileDir || config.chromeProfile;
        return resolveCredentials({
            authToken: opts.authToken,
            ct0: opts.ct0,
            cookieSource,
            chromeProfile,
            firefoxProfile: opts.firefoxProfile || config.firefoxProfile,
            cookieTimeoutMs: resolveCookieTimeoutFromOptions(opts),
        });
    }
    function loadMedia(opts) {
        if (opts.media.length === 0) {
            return [];
        }
        const specs = [];
        for (const [index, path] of opts.media.entries()) {
            const mime = detectMime(path);
            if (!mime) {
                throw new Error(`Unsupported media type for ${path}. Supported: jpg, jpeg, png, webp, gif, mp4, mov`);
            }
            const buffer = readFileSync(path);
            specs.push({ path, mime, buffer, alt: opts.alts[index] });
        }
        const videoCount = specs.filter((m) => m.mime.startsWith('video/')).length;
        if (videoCount > 1) {
            throw new Error('Only one video can be attached');
        }
        if (videoCount === 1 && specs.length > 1) {
            throw new Error('Video cannot be combined with other media');
        }
        if (specs.length > 4) {
            throw new Error('Maximum 4 media attachments');
        }
        return specs;
    }
    function printTweets(tweets, opts = {}) {
        if (opts.json) {
            console.log(JSON.stringify(tweets, null, 2));
            return;
        }
        if (tweets.length === 0) {
            console.log(opts.emptyMessage ?? 'No tweets found.');
            return;
        }
        const useEmoji = output.emoji && !output.plain;
        const articleLabel = useEmoji ? '📰' : 'Article:';
        const mediaLabel = (type) => {
            if (useEmoji) {
                return type === 'video' ? '🎬' : type === 'animated_gif' ? '🔄' : '🖼️';
            }
            return type === 'video' ? 'VIDEO:' : type === 'animated_gif' ? 'GIF:' : 'PHOTO:';
        };
        const quotePrefix = useEmoji ? { top: '┌─', mid: '│ ', bot: '└─' } : { top: '> ', mid: '> ', bot: '> ' };
        for (const tweet of tweets) {
            console.log(`\n@${tweet.author.username} (${tweet.author.name}):`);
            // Display tweet text, with article indicator if present
            if (tweet.article) {
                // Full body mode: text starts with article title (from extractArticleText)
                // Preview mode: text is short tweet intro that doesn't start with title
                const hasFullBody = tweet.text.startsWith(tweet.article.title);
                if (hasFullBody) {
                    console.log(`${articleLabel} ${tweet.text}`);
                }
                else {
                    console.log(`${articleLabel} ${tweet.article.title}`);
                    if (tweet.article.previewText) {
                        console.log(`   ${tweet.article.previewText}`);
                    }
                }
            }
            else {
                console.log(tweet.text);
            }
            // Display media attachments
            if (tweet.media && tweet.media.length > 0) {
                for (const m of tweet.media) {
                    console.log(`${mediaLabel(m.type)} ${m.url}`);
                }
            }
            // Display quoted tweet
            if (tweet.quotedTweet) {
                console.log(`${quotePrefix.top} QT @${tweet.quotedTweet.author.username}:`);
                const qtText = tweet.quotedTweet.article
                    ? `${articleLabel} ${tweet.quotedTweet.article.title}`
                    : tweet.quotedTweet.text;
                // Indent and truncate quoted tweet text
                const maxLen = 280;
                const truncated = qtText.length > maxLen ? `${qtText.slice(0, maxLen)}...` : qtText;
                for (const line of truncated.split('\n').slice(0, 4)) {
                    console.log(`${quotePrefix.mid}${line}`);
                }
                // Display quoted tweet media
                if (tweet.quotedTweet.media && tweet.quotedTweet.media.length > 0) {
                    for (const m of tweet.quotedTweet.media) {
                        console.log(`${quotePrefix.mid}${mediaLabel(m.type)} ${m.url}`);
                    }
                }
                console.log(`${quotePrefix.bot} https://x.com/${tweet.quotedTweet.author.username}/status/${tweet.quotedTweet.id}`);
            }
            if (tweet.createdAt) {
                console.log(`${l('date')}${tweet.createdAt}`);
            }
            const tweetUrl = `https://x.com/${tweet.author.username}/status/${tweet.id}`;
            console.log(`${l('url')}${hyperlink(tweetUrl, tweetUrl, output)}`);
            if (opts.showSeparator ?? true) {
                console.log('─'.repeat(50));
            }
        }
    }
    function printTweetsResult(result, opts) {
        const tweets = result.tweets ?? [];
        if (opts.json && opts.usePagination) {
            console.log(JSON.stringify({ tweets, nextCursor: result.nextCursor ?? null }, null, 2));
            return;
        }
        printTweets(tweets, { json: opts.json, emptyMessage: opts.emptyMessage });
    }
    return {
        isTty,
        getOutput: () => output,
        colors,
        p,
        l,
        config,
        applyOutputFromCommand,
        resolveTimeoutFromOptions,
        resolveQuoteDepthFromOptions,
        resolveCredentialsFromOptions,
        loadMedia,
        printTweets,
        printTweetsResult,
        extractTweetId,
    };
}
//# sourceMappingURL=shared.js.map