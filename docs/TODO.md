# TODO

Deferred findings from the stage 1 final review (2026-09-28). None blocks stage 1; pick them up when planning stage 2.

## Behavior

- [ ] **Validate all `expand_comments` tokens before the first request** (`internal/tools/tools.go`, `expandComments`). Today `["m:a", "x:bad"]` queries Reddit for `m:a` before failing, and a failure on one token discards fragments already loaded. Export a small token parser from `internal/reddit`; optionally return successful fragments with a note about failed ones.
- [ ] **Fix link parsing for subreddits named `gallery` or `comments`** (`internal/reddit/ref.go`). `https://www.reddit.com/r/gallery/comments/abc123/x/` resolves to id `comments`. Match `comments`/`gallery` only at their known positions (`segs[0]`, or `segs[2]` after `r/<sub>` or `u|user/<name>`).
- [ ] **Accept user-profile share links** `/u/<name>/s/<code>` (`internal/reddit/ref.go`): allow `u` and `user` alongside `r`.
- [ ] **Handle 429/5xx from the token endpoint** (`internal/reddit/client.go`, `accessToken`): map 429 to `RateLimitError`, retry 5xx once.

## Messages and output

- [ ] **Show the "subreddit name may be wrong" hint only for subreddit searches** (`internal/tools/tools.go`, `describe`); today `get_post` and `expand_comments` get it too.
- [ ] **Stop duplicating "reddit request failed:"** in the default error branch (`describe` + `client.go`, `doAPI`), which also exposes the full URL.
- [ ] **Map 5xx after the retry to a model-facing message** ("Reddit is having problems, try again later") instead of "unexpected HTTP status 502".
- [ ] **Omit hidden subscriber counts** (`internal/tools/render.go`, `renderSubreddits`): `subscribers: null` renders as "0 subscribers". Needs a nil-able field and an updated `subreddits.golden`.

## Docs and tests

- [ ] **State in the README that Go 1.27+ is required** for `go install` (the code uses `encoding/json/v2`).
- [ ] **Replace or supplement hand-written fixtures with trimmed real Reddit responses** once API credentials exist.
- [ ] **Check against the live API:**
  - [ ] what a nonexistent subreddit in `/r/<x>/search` returns (redirect target, relative or absolute);
  - [ ] how long the top-level `more.children` list is on a large thread;
  - [ ] whether `/s/` share links resolve (`REDDIT_TEST_SHARE_URL`);
  - [ ] whether app-only search returns NSFW results with `include_over_18=on`.
