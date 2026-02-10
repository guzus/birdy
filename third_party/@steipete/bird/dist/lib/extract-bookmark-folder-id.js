/**
 * Extract bookmark folder (collection) ID from an X/Twitter bookmarks URL.
 */
const BOOKMARK_FOLDER_URL_REGEX = /(?:twitter\.com|x\.com)\/i\/bookmarks\/(\d+)/i;
const BOOKMARK_FOLDER_ID_REGEX = /^\d{5,}$/;
export function extractBookmarkFolderId(input) {
    const trimmed = input.trim();
    if (!trimmed) {
        return null;
    }
    const urlMatch = BOOKMARK_FOLDER_URL_REGEX.exec(trimmed);
    if (urlMatch) {
        return urlMatch[1];
    }
    if (BOOKMARK_FOLDER_ID_REGEX.test(trimmed)) {
        return trimmed;
    }
    return null;
}
//# sourceMappingURL=extract-bookmark-folder-id.js.map