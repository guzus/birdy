# Birdy Jev privacy policy

Effective October 23, 2026.

Birdy Jev is a Chrome extension that works with **birdy-jev, a server you run yourself**. The extension's author operates no server for it, receives none of your data, and runs no analytics or ads.

## What the extension reads

- **Posts on x.com near your screen:** their visible text, author, and link, so they can be judged.
- **Your Following timeline, for Radar:** the newest 60 posts, read by the Birdy native host on your computer.
- **Two X cookies, for Radar:** `auth_token` and `ct0`. They are passed only to the native host on the same computer, which uses them to read your timeline as you. They are never stored and never sent to any server.
- **What you type into Ask Birdy.**

## Where it goes

- **Your birdy-jev server**, at the address you set in the extension. It stores each post's judgement, including the post text, and each Radar check's topics and post ids, in your own database.
- **The model providers your server uses:** TypeSafe (Jev) judges new posts, and Anthropic (Claude, through Claude Code) finds Radar's topics and answers Ask Birdy. Each handles the data under its own terms.
- **X**, when the native host reads your timeline with your session.

Posts labelled as ads are hidden in the page and sent nowhere.

## What stays in your browser

Your settings, your server's address and token, the latest Radar topics, and the Ask Birdy conversation. The conversation is cleared when the browser closes.

## What never happens

Your data is not sold or shared with anyone beyond the services above, is not used for advertising, and is not used to decide creditworthiness. The extension loads no remote code.

## Your control

Turn Birdy Jev, Radar, or ad hiding off in the popup, or remove the extension. Data on your server is yours to delete there.

## Chrome Web Store User Data Policy

Birdy Jev's use of information complies with the [Chrome Web Store User Data Policy](https://developer.chrome.com/docs/webstore/program-policies/user-data-faq), including the Limited Use requirements.

## Contact

Open an issue at [github.com/guzus/birdy/issues](https://github.com/guzus/birdy/issues).
