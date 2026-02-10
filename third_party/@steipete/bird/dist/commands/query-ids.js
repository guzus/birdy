import { getFeatureOverridesSnapshot, refreshFeatureOverridesCache, } from '../lib/runtime-features.js';
import { runtimeQueryIds } from '../lib/runtime-query-ids.js';
function countFeatureOverrides(overrides) {
    let count = 0;
    if (overrides.global) {
        count += Object.keys(overrides.global).length;
    }
    if (overrides.sets) {
        for (const setOverrides of Object.values(overrides.sets)) {
            count += Object.keys(setOverrides).length;
        }
    }
    return count;
}
export function registerQueryIdsCommand(program, ctx) {
    program
        .command('query-ids')
        .description('Show or refresh cached Twitter GraphQL query IDs')
        .option('--json', 'Output as JSON')
        .option('--fresh', 'Force refresh (downloads X client bundles)', false)
        .action(async (cmdOpts) => {
        const operations = [
            'CreateTweet',
            'CreateRetweet',
            'FavoriteTweet',
            'TweetDetail',
            'SearchTimeline',
            'UserArticlesTweets',
            'Bookmarks',
            'Following',
            'Followers',
            'Likes',
        ];
        if (cmdOpts.fresh) {
            console.error(`${ctx.p('info')}Refreshing GraphQL query IDs…`);
            await runtimeQueryIds.refresh(operations, { force: true });
            console.error(`${ctx.p('info')}Refreshing feature overrides…`);
            await refreshFeatureOverridesCache();
        }
        const featureSnapshot = getFeatureOverridesSnapshot();
        const info = await runtimeQueryIds.getSnapshotInfo();
        if (!info) {
            if (cmdOpts.json) {
                console.log(JSON.stringify({
                    cached: false,
                    cachePath: runtimeQueryIds.cachePath,
                    featuresPath: featureSnapshot.cachePath,
                    features: featureSnapshot.overrides,
                }, null, 2));
                return;
            }
            console.log(`${ctx.p('warn')}No cached query IDs yet.`);
            console.log(`${ctx.p('info')}Run: bird query-ids --fresh`);
            console.log(`features_path: ${featureSnapshot.cachePath}`);
            return;
        }
        if (cmdOpts.json) {
            console.log(JSON.stringify({
                cached: true,
                cachePath: info.cachePath,
                fetchedAt: info.snapshot.fetchedAt,
                isFresh: info.isFresh,
                ageMs: info.ageMs,
                ids: info.snapshot.ids,
                discovery: info.snapshot.discovery,
                featuresPath: featureSnapshot.cachePath,
                features: featureSnapshot.overrides,
            }, null, 2));
            return;
        }
        console.log(`${ctx.p('ok')}GraphQL query IDs cached`);
        console.log(`path: ${info.cachePath}`);
        console.log(`fetched_at: ${info.snapshot.fetchedAt}`);
        console.log(`fresh: ${info.isFresh ? 'yes' : 'no'}`);
        console.log(`ops: ${Object.keys(info.snapshot.ids).length}`);
        console.log(`features_path: ${featureSnapshot.cachePath}`);
        console.log(`features: ${countFeatureOverrides(featureSnapshot.overrides)}`);
    });
}
//# sourceMappingURL=query-ids.js.map