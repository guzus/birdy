/**
 * Browser cookie extraction for Twitter authentication.
 * Delegates to @steipete/sweet-cookie for Safari/Chrome/Firefox reads.
 */
export interface TwitterCookies {
    authToken: string | null;
    ct0: string | null;
    cookieHeader: string | null;
    source: string | null;
}
export interface CookieExtractionResult {
    cookies: TwitterCookies;
    warnings: string[];
}
export type CookieSource = 'safari' | 'chrome' | 'firefox';
export declare function extractCookiesFromSafari(): Promise<CookieExtractionResult>;
export declare function extractCookiesFromChrome(profile?: string): Promise<CookieExtractionResult>;
export declare function extractCookiesFromFirefox(profile?: string): Promise<CookieExtractionResult>;
/**
 * Resolve Twitter credentials from multiple sources.
 * Priority: CLI args > environment variables > browsers (ordered).
 */
export declare function resolveCredentials(options: {
    authToken?: string;
    ct0?: string;
    cookieSource?: CookieSource | CookieSource[];
    chromeProfile?: string;
    firefoxProfile?: string;
    cookieTimeoutMs?: number;
}): Promise<CookieExtractionResult>;
//# sourceMappingURL=cookies.d.ts.map