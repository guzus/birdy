import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
export const FALLBACK_VERSION = 'unknown';
const REF_PREFIX_REGEX = /^ref:\s*/i;
const LINE_SPLIT_REGEX = /\r?\n/;
const GITDIR_REGEX = /gitdir:\s*(.+)\s*$/i;
function readPackageVersionFromJsonFile(candidate) {
    try {
        const raw = fs.readFileSync(candidate, 'utf8');
        const json = JSON.parse(raw);
        if (json && typeof json.version === 'string' && json.version.trim().length > 0) {
            return json.version.trim();
        }
        return null;
    }
    catch {
        return null;
    }
}
function readVersionFromTextFile(candidate) {
    try {
        const raw = fs.readFileSync(candidate, 'utf8').trim();
        return raw.length > 0 ? raw : null;
    }
    catch {
        return null;
    }
}
function resolveStartDir(importMetaUrl) {
    if (typeof importMetaUrl === 'string' && importMetaUrl.trim().length > 0) {
        try {
            return path.dirname(fileURLToPath(importMetaUrl));
        }
        catch {
            // ignore
        }
    }
    if (typeof __dirname === 'string' && __dirname.length > 0) {
        return __dirname;
    }
    return process.cwd();
}
export function resolvePackageVersion(importMetaUrl) {
    const injected = typeof process !== 'undefined' && typeof process.env.BIRD_VERSION === 'string'
        ? process.env.BIRD_VERSION.trim()
        : '';
    if (injected.length > 0) {
        return injected;
    }
    let dir = resolveStartDir(importMetaUrl);
    for (let i = 0; i < 10; i += 1) {
        const version = readPackageVersionFromJsonFile(path.join(dir, 'package.json')) ??
            readVersionFromTextFile(path.join(dir, 'VERSION'));
        if (version) {
            return version;
        }
        const parent = path.dirname(dir);
        if (parent === dir) {
            break;
        }
        dir = parent;
    }
    return FALLBACK_VERSION;
}
function truncateSha(sha, length = 8) {
    const trimmed = sha.trim();
    if (!trimmed) {
        return '';
    }
    if (trimmed.length <= length) {
        return trimmed;
    }
    return trimmed.slice(0, length);
}
function resolveGitShaFromGitDir(gitDir) {
    const headPath = path.join(gitDir, 'HEAD');
    let head = '';
    try {
        head = fs.readFileSync(headPath, 'utf8').trim();
    }
    catch {
        return null;
    }
    if (!head) {
        return null;
    }
    if (!head.startsWith('ref:')) {
        const sha = truncateSha(head);
        return sha.length > 0 ? sha : null;
    }
    const ref = head.replace(REF_PREFIX_REGEX, '').trim();
    if (!ref) {
        return null;
    }
    const refPath = path.join(gitDir, ref);
    try {
        const sha = truncateSha(fs.readFileSync(refPath, 'utf8'));
        return sha.length > 0 ? sha : null;
    }
    catch {
        // fall through to packed-refs
    }
    const packedRefsPath = path.join(gitDir, 'packed-refs');
    try {
        const packed = fs.readFileSync(packedRefsPath, 'utf8');
        const lines = packed.split(LINE_SPLIT_REGEX);
        for (const line of lines) {
            if (!line || line.startsWith('#') || line.startsWith('^')) {
                continue;
            }
            const [shaRaw, refName] = line.split(' ');
            if (refName?.trim() === ref) {
                const sha = truncateSha(shaRaw ?? '');
                return sha.length > 0 ? sha : null;
            }
        }
    }
    catch {
        // ignore
    }
    return null;
}
export function resolveGitSha(importMetaUrl) {
    const injected = typeof process !== 'undefined' && typeof process.env.BIRD_GIT_SHA === 'string'
        ? process.env.BIRD_GIT_SHA.trim()
        : '';
    if (injected.length > 0) {
        return truncateSha(injected);
    }
    let dir = resolveStartDir(importMetaUrl);
    for (let i = 0; i < 10; i += 1) {
        const dotGit = path.join(dir, '.git');
        try {
            const stat = fs.statSync(dotGit);
            if (stat.isDirectory()) {
                const sha = resolveGitShaFromGitDir(dotGit);
                if (sha) {
                    return sha;
                }
            }
            else if (stat.isFile()) {
                const txt = fs.readFileSync(dotGit, 'utf8');
                const match = GITDIR_REGEX.exec(txt);
                const gitDir = match?.[1]?.trim();
                if (gitDir) {
                    const resolved = path.isAbsolute(gitDir) ? gitDir : path.resolve(dir, gitDir);
                    const sha = resolveGitShaFromGitDir(resolved);
                    if (sha) {
                        return sha;
                    }
                }
            }
        }
        catch {
            // ignore
        }
        const parent = path.dirname(dir);
        if (parent === dir) {
            break;
        }
        dir = parent;
    }
    return null;
}
export function formatVersionLine(importMetaUrl) {
    const version = resolvePackageVersion(importMetaUrl);
    const sha = resolveGitSha(importMetaUrl);
    return sha ? `${version} (${sha})` : version;
}
export function getCliVersion() {
    return formatVersionLine(import.meta.url);
}
//# sourceMappingURL=version.js.map