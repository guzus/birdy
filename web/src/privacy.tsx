// The privacy notice for this site's optional usage counts (served at
// /privacy). The counts exist only when the operator turns them on, so the
// notice says so: it is accurate for any instance.

export const privacyPath = '/privacy';

export function PrivacyPage() {
  return (
    <div className="h-full overflow-y-auto">
      <main className="mx-auto w-full max-w-[640px] p-4 sm:p-6 text-sm leading-relaxed text-text">
        <header className="flex items-center justify-between border-b border-border pb-3 mb-4">
          <h1 className="m-0 text-lg font-semibold tracking-tight">Privacy</h1>
          <a href="/" className="text-xs text-text-dim no-underline hover:text-text">Back to Birdy</a>
        </header>
        <h2 className="mt-0 mb-2 text-base font-semibold">Usage counts</h2>
        <p className="my-2">
          Usage counts are off unless the site's operator turns them on. When they are on, this site keeps,
          to run the service, daily counts of how it is used:
        </p>
        <ul className="my-2 pl-5 list-disc space-y-1.5">
          <li>
            Requests per UTC day by surface (chat, scan briefings and commands, with invited users and
            visitors counted apart) and by the engine that answered.
          </li>
          <li>
            To count distinct visitors, a salted hash (HMAC-SHA256) of the IP address each request came
            from. The key for these hashes changes on the first of every month (it is derived from a
            secret): within a calendar month the same address gets the same hash every day, and the next
            month it gets a different one. Hashes are deleted after 35 days.
          </li>
          <li>How many X accounts the site uses and how often they hit rate limits, as numbers only.</li>
        </ul>
        <p className="my-2">
          No IP addresses are kept, and no chat, scan or command content is stored for these counts. Nothing
          is shared or sold, and there is no third-party analytics.
        </p>
        <p className="my-2 text-text-dim text-xs">This notice covers the usage counts.</p>
      </main>
    </div>
  );
}
