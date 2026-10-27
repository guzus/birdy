# Birdy Jev privacy policy

Effective October 27, 2026.

Birdy Jev is a Chrome extension that makes X quieter. It works with **birdy-jev, a server operated by Birdy Jev's developer at `birdy.guzus.xyz`**. There are no accounts, no analytics and no ads.

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
