export type FeatureOverrides = {
    global?: Record<string, boolean>;
    sets?: Record<string, Record<string, boolean>>;
};
type NormalizedFeatureOverrides = {
    global: Record<string, boolean>;
    sets: Record<string, Record<string, boolean>>;
};
export type FeatureOverridesSnapshot = {
    cachePath: string;
    overrides: FeatureOverrides;
};
export declare function loadFeatureOverrides(): NormalizedFeatureOverrides;
export declare function getFeatureOverridesSnapshot(): FeatureOverridesSnapshot;
export declare function applyFeatureOverrides(setName: string, base: Record<string, boolean>): Record<string, boolean>;
export declare function refreshFeatureOverridesCache(): Promise<FeatureOverridesSnapshot>;
export declare function clearFeatureOverridesCache(): void;
export {};
//# sourceMappingURL=runtime-features.d.ts.map