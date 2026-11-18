# Birdy Jev privacy policy

Effective November 19, 2026.

Birdy Jev is a Chrome extension that makes X quieter. It works with **birdy-jev, a server operated by Birdy Jev's developer at `birdy.guzus.xyz`**. There are no accounts, no third-party analytics and no ads.

## What the extension reads

- **Posts on x.com near your screen:** their visible text, author, link, time, language, image links, and public counts (replies, reposts, likes, views), so they can be judged. The server keeps only part of this (see below).
- **Your Following timeline and two X cookies, for Radar only:** Radar is not offered to Web Store users yet. Before reading anything for Radar, the extension asks the server, which says no, so the cookies (`auth_token`, `ct0`) are never read. Where Radar does run (for the server's owner), the cookies go only to a program on the same computer and never to any server.
- **What you type into Ask Birdy,** which is likewise not offered to Web Store users yet.
- **Your "Slop" and "Not slop" marks** on posts, when you click them.

## Where it goes

- **The birdy-jev server at birdy.guzus.xyz.** It keeps each post it judges for you (its text, quoted text, author and link) with the judgement, so the next person shown the same post gets the answer without a new judgement. It does not keep the counts, times, languages or image links it receives, and it does not record which installation sent which post for judging. Your marks (the post's id, your verdict, your threshold, and the judgement you were shown) are kept with your installation's number, to measure how well the filter works. Judgements and marks are kept for analysis.
- **TypeSafe (Jev),** which judges posts the server has not judged before. Only the text, quoted text and author go to TypeSafe, under its own terms.
- **Cloudflare,** which carries traffic to the server.

Posts labelled as ads are hidden in the page and sent nowhere.

## Your installation

On first use the extension registers itself with the server and gets a random key. The key identifies this installation, not you, and the server stores only a hash of it. With it the server keeps: an installation number, the extension's ID and version, when it was registered and last seen, how many posts were judged for it and how many marks it sent each day (to enforce daily limits and show your count in the popup), your marks, and a keyed hash of the network address it registered from (to limit registrations; the address itself is not stored).

## Usage counts

To run the service, the server keeps daily counts of the requests made to its API.

- **What is counted:** every request to the server's API (the addresses under `/v1/`), from the extension and from anyone using the server's public post lookup. That covers judging posts, marks, the popup's counts, registration, the Radar and Ask Birdy checks, and post lookups. Requests from Web Store installations are included. For each UTC day the server counts requests by kind (the route, such as `/v1/classify`, never a post ID or other value from the request), by status class (succeeded, refused, rate-limited or failed), and by whether the answer came from the cache.
- **Distinct users:** to count distinct users per day, the server keeps, for each day, a salted hash (HMAC-SHA256) of the IP address each request came from. The salt changes on the first of every month, so hashes cannot be matched from one month to the next. Each day's hashes are deleted after 35 days.
- **Not stored:** IP addresses themselves, request bodies, query contents and post IDs. The counts and hashes are not linked to your installation's number or key.
- **Not shared:** the counts are used only to run the service. They are not shared or sold, and no third-party analytics service receives them.

## What stays in your browser

Your settings, the server's address, the installation key, and (where Radar runs) the latest Radar topics and the Ask Birdy conversation, which is cleared when the browser closes.

## What never happens

Your data is not sold or shared with anyone beyond the services above, is not used for advertising, and is not used to decide creditworthiness. The extension loads no remote code.

## Your control

Turn Birdy Jev or ad hiding off in the popup, or remove the extension. To have your installation's record deleted, open an issue with the installation number shown under **Settings** in the popup.

## Chrome Web Store User Data Policy

Birdy Jev's use of information complies with the [Chrome Web Store User Data Policy](https://developer.chrome.com/docs/webstore/program-policies/user-data-faq), including the Limited Use requirements.

## Contact

Open an issue at [github.com/guzus/birdy/issues](https://github.com/guzus/birdy/issues).
