# Backlog

Deferred findings from the stage 1 final review (2026-09-28). None blocks stage 1; pick them up when planning stage 2.

## Behavior

1. **`expand_comments` validates tokens lazily** (`internal/tools/tools.go`, `expandComments`). With `["m:a", "x:bad"]` Reddit is queried for `m:a` before the input error, and fragments already loaded are discarded. A 404/5xx on one token also loses the others. Validate all tokens before the first request (export a small token parser from `internal/reddit`); optionally return the successful fragments with a note about the failed ones.
2. **Subreddits named `gallery` or `comments` break link parsing** (`internal/reddit/ref.go`). `https://www.reddit.com/r/gallery/comments/abc123/x/` resolves to id `comments`. Match `comments`/`gallery` only at their known positions (`segs[0]`, or `segs[2]` after `r/<sub>` or `u|user/<name>`).
3. **User-profile share links are rejected** (`internal/reddit/ref.go`). `/u/<name>/s/<code>` gets "cannot find a Reddit post id". Accept `u` and `user` alongside `r`.
4. **Token endpoint 429/5xx are not special-cased** (`internal/reddit/client.go`, `accessToken`). A 429 becomes a generic `StatusError` instead of `RateLimitError`, and a 5xx is not retried.

## Messages and output

5. **Error message polish** (`internal/tools/tools.go`, `describe`):
   - the `ErrUnexpectedResponse` hint "the subreddit name may be wrong" is also shown for `get_post` and `expand_comments`;
   - the default branch prefixes "reddit request failed: " to errors that already carry it (`client.go`, `doAPI`), duplicating it and exposing the full URL;
   - 5xx after the retry reads "unexpected HTTP status 502" instead of a model-facing "Reddit is having problems, try again later" (the spec's upstream-failure category).
6. **Hidden subscriber counts render as "0 subscribers"** (`internal/tools/render.go`, `renderSubreddits`). `subscribers: null` should omit the count; needs a nil-able field and an updated `subreddits.golden`.

## Docs and tests

7. **README does not state that Go 1.27+ is required** for `go install` (the code uses `encoding/json/v2`).
8. **Fixtures are hand-written**, not derived from recorded Reddit responses as the spec intended. Once API credentials exist, replace or supplement them with trimmed real responses, and check against the live API:
   - what a nonexistent subreddit in `/r/<x>/search` returns (redirect target, relative or absolute);
   - how long the top-level `more.children` list is on a large thread;
   - whether `/s/` share links resolve (`REDDIT_TEST_SHARE_URL`);
   - whether app-only search returns NSFW results with `include_over_18=on`.
