# mcp-reddit — stage 1 design

_Date: 2026-09-28 · Status: approved in brainstorming, pending written-spec review_

## Goal

A read-only MCP server, written in Go, that lets an AI assistant (Claude Code, Claude Desktop, other MCP clients) research Reddit on the user's request. Two scenarios drive every decision:

1. **Topic research.** "Find out what people think about X." The assistant searches posts, picks promising threads, reads their top comments and summarizes opinions.
2. **Post analysis.** The user points at a specific post (any link form, including mobile share links). The assistant reads it, then finds related discussions elsewhere to widen the picture.

The project is open source. Each user runs the binary locally over stdio with their own Reddit credentials.

## Constraints discovered during design

- **Reddit closed self-service API keys on 2025-11-11.** `reddit.com/prefs/apps` no longer issues credentials. Access requires an application through Reddit Developer Support under the Responsible Builder Policy, with manual review. The maintainer has no credentials yet and has applied (application text and `PRIVACY.md` prepared separately).
- **Unauthenticated `.json` endpoints return 403 since late May 2026.** An anonymous fallback is therefore impossible. RSS still works but lacks scores, comment trees, duplicates and "more comments", and is rate limited to about 1 request per minute. It is rejected.
- **Responsible Builder Policy:** no model training on Reddit data, no commercialization, and stored data should be deleted within 48 hours. The server persists nothing, which `PRIVACY.md` states.
- **No official comment search exists** (Pushshift is closed). Opinions are gathered by reading threads found through post search.

## Scope

**In stage 1:** five read-only tools: `search_posts`, `get_post`, `expand_comments`, `get_related_posts`, `search_subreddits`.

**Explicitly out of scope, possible later stages:**
- `browse_subreddit`
- `get_user`
- server-side `search_comments`, to be added only if reading threads proves too context-hungry in practice
- an in-memory response cache
- GoReleaser binaries
- OS keychain support
- any write action

## Decisions

| Topic | Decision |
|---|---|
| MCP SDK | Official `github.com/modelcontextprotocol/go-sdk`: typed tool handlers with schemas inferred from input structs, stdio transport |
| Reddit client | Own thin client on `net/http` (existing Go Reddit libraries are unmaintained) |
| Auth | OAuth application-only (`client_credentials`) only; the server refuses to start without credentials |
| Credentials | Environment variables only; secret managers plug in through a wrapper command (see Configuration) |
| Output | Compact plain text, no structured output (token efficiency) |
| Persistence | None; nothing is ever written to disk |
| Module path | `github.com/blunext/mcp-reddit` |
| JSON | `encoding/json/v2` (and `encoding/json/jsontext`) everywhere in our code; never `encoding/json` v1. Custom decoding uses `UnmarshalJSONFrom(*jsontext.Decoder)` |

## Architecture

```
cmd/mcp-reddit/main.go   config from env, wiring, run server on stdio; logs to stderr
internal/reddit/         Reddit API client; knows nothing about MCP
internal/tools/          MCP tools: input structs, handlers, text formatting
```

**Flow:** the MCP client calls a tool. `go-sdk` decodes the arguments into the tool's input struct, and the handler in `tools` calls the `reddit` client, which returns domain types (`Post`, `Comment`, `MoreMarker`, `Subreddit`). The handler renders them as compact text and returns it as `TextContent`.

**Boundaries:**
- `internal/reddit` hides Reddit's wire format: `Listing`/`Thing` envelopes, `t1_`/`t3_`/`t5_` prefixes, `replies` sometimes being `""`, and the flat `morechildren` responses. It exposes domain types and methods: `SearchPosts`, `GetPost`, `MoreComments`, `Duplicates`, `SearchSubreddits`, `ResolvePostRef`.
- `internal/tools` depends on the client through a small interface declared in `tools`, so handlers and formatting are tested with a fake.
- `main.go` reads `REDDIT_CLIENT_ID`, `REDDIT_CLIENT_SECRET`, `REDDIT_USERNAME` and an optional `REDDIT_USER_AGENT`. The user agent follows Reddit's API rules: `<GOOS>:github.com/blunext/mcp-reddit:<version> (by /u/<REDDIT_USERNAME>)`; `REDDIT_USER_AGENT` replaces it entirely, and then `REDDIT_USERNAME` is not needed. If a credential or the username is missing, the server exits non-zero with a message pointing to the README. stdout belongs to the MCP protocol, so all logging goes to stderr.
- Every tool carries the annotation `ReadOnlyHint: true`.

## Tools

**Common conventions**
- Every item shows the `id` needed for follow-up calls.
- Dates are shown as `YYYY-MM-DD`.
- NSFW items are marked `[NSFW]`, never filtered.
- Removed or deleted content is shown as `[removed]` or `[deleted]` without a body.

### `search_posts`
- Input:
  - `query` (required)
  - `subreddit` (optional, restricts the search)
  - `sort`: `relevance` | `hot` | `top` | `new` | `comments`, default `relevance`
  - `time`: `hour` | `day` | `week` | `month` | `year` | `all`, default `all`
  - `limit`: default 10, max 50
  - `after`: pagination cursor
- Endpoint: `/search`, or `/r/{sub}/search?restrict_sr=1`, with `type=link`.
- Output per post: title, `r/sub`, score, comment count, date, author, `id`, permalink, and the first ~300 characters of the body. A trailing `after` cursor appears when more results exist.

### `get_post`
- Input:
  - `post`: a bare id, `t3_…`, a full URL, `redd.it/…`, or a share link `reddit.com/r/…/s/…`
  - `comment_sort`: `top` | `best` | `new` | `controversial` | `old` | `qa`, default `top`
  - `comment_limit`: default 50, max 200
  - `depth`: default 4
- Endpoint: `/comments/{id}` with `sort`, `limit` and `depth`.
- Output:
  - a post header followed by the full body, truncated at ~8000 characters with a note;
  - an indented comment tree, one comment per line group as `[score] u/author (id): body`, each body truncated at ~1500 characters;
  - collapsed branches rendered as `[+N more replies → expand_comments token: <token>]`.

### `expand_comments`
- Input: `post` (as in `get_post`) and `tokens` (a list of tokens taken from the markers).
- A token encodes one of Reddit's two collapse kinds:
  - "load more comments" (a list of child ids), resolved via `/api/morechildren` (max 100 ids per call, chunked);
  - "continue this thread" (depth cut-off), resolved via `/comments/{post}?comment={parent_id}`.
- The server picks the endpoint. The caller never needs to know which kind it has.
- Output: the same tree format, each fragment headed with the parent comment it replies to.

### `get_related_posts`
- Input: `post` (as in `get_post`) and `limit` (default 10, max 50).
- Endpoint: `/duplicates/{id}`.
- Output: same list format as `search_posts`.
- The tool description tells the model this finds other submissions of the same link. For text posts, it should call `search_posts` with keywords instead.

### `search_subreddits`
- Input: `query` (required) and `limit` (default 10, max 50).
- Endpoint: `/subreddits/search`.
- Output per subreddit: `r/name`, subscriber count, short public description, `[NSFW]` if applicable.

## Reddit client

**Auth**
- Get a token with `POST https://www.reddit.com/api/v1/access_token`, `grant_type=client_credentials`, and HTTP Basic auth using the client id and secret.
- Keep the token in memory only, behind a mutex. Refresh lazily 60 s before expiry.
- On a 401 from the API, refresh once and retry the request once.
- A failure to obtain a token is reported as an auth error.

**Requests**
- Base URL `https://oauth.reddit.com`.
- Every request sends `raw_json=1` (no HTML entities) and the User-Agent.
- Per-request timeout of 15 s, derived from the tool call's context.
- Base URLs, the `http.Client` and the clock are injectable for tests.

**Rate limiting**
- Track `X-Ratelimit-Remaining` and `X-Ratelimit-Reset`.
- When the budget is exhausted: if the reset is ≤ 5 s away, wait. Otherwise fail immediately with a rate-limit error that carries the wait time. A 429 response is handled the same way.
- Tool calls never block for long periods.
- 5xx responses are retried once after a short backoff.

**Post references**
- Ids are extracted locally from `/comments/{id}/`, `redd.it/{id}`, `t3_{id}` and bare ids.
- Share links (`/s/…`) are resolved by a single GET with redirects disabled, reading the `Location` header.
- **Open risk:** that request goes to `www.reddit.com` and may be blocked with a 403. In that case `get_post` returns an error asking for the full post URL. This is verified once credentials exist.

**Parsing**
- `/comments/{id}` returns a two-element array (post listing, comment listing).
- `replies` is either a listing or `""`; a custom `UnmarshalJSONFrom` (json/v2) handles both.
- `more` things become `MoreMarker{Count, Token}`.
- `/api/morechildren` returns a flat list with `parent_id`, which the client reassembles into trees.

## Errors

Reddit-side problems are returned as tool errors (`IsError: true`) with messages written for the model, never as protocol errors. The model can then retry or explain what happened. Categories:

- **Invalid input:** an unparseable post reference, or an unknown sort value.
- **Not accessible:** post removed, subreddit private, banned or quarantined (403/404).
- **Rate limited:** includes the seconds to wait.
- **Auth failure:** invalid or revoked credentials; points to the README.
- **Upstream/network failure:** timeouts and 5xx after the retry.

## Testing

- **`internal/reddit`:** `httptest.Server` with JSON fixtures in `testdata/`, derived from real recorded Reddit responses (PRAW's test cassettes) and trimmed. Covers:
  - post-reference parsing for all link forms;
  - token fetch, lazy refresh and the 401-retry path;
  - rate-limit handling with an injected clock;
  - comment trees including `replies: ""` and both `more` kinds;
  - `morechildren` reassembly;
  - error mapping.
- **`internal/tools`:** handlers against a fake client, with golden-file tests for rendered output (`testdata/*.golden`, regenerated with `-update`).
- **End-to-end:** tools called through the real MCP protocol using `go-sdk` in-memory transports.
- **Integration:** tests against the live API that run only when `REDDIT_CLIENT_ID` and `REDDIT_CLIENT_SECRET` are set, and `t.Skip` otherwise.

## Distribution and CI

- Install with `go install github.com/blunext/mcp-reddit/cmd/mcp-reddit@latest`.
- GitHub Actions runs `go test ./...`, `go vet ./...` and `golangci-lint`.
- `.gitignore` covers `.idea/` and build output.

## Configuration (README content)

- How to obtain credentials: the Developer Support application and the Responsible Builder Policy. It states plainly that approval is manual and can take weeks.
- Environment variables: `REDDIT_CLIENT_ID`, `REDDIT_CLIENT_SECRET`, `REDDIT_USERNAME`, optional `REDDIT_USER_AGENT`.
- Plain setup for Claude Code:
  ```sh
  claude mcp add reddit -e REDDIT_CLIENT_ID=… -e REDDIT_CLIENT_SECRET=… -- mcp-reddit
  ```
  Note that this stores the secret in plain text in the client config.
- Recommended setup with a secret-manager wrapper. For gopass:
  ```sh
  claude mcp add reddit -- sh -c 'REDDIT_CLIENT_ID="$(gopass show -o reddit/client-id)" REDDIT_CLIENT_SECRET="$(gopass show -o reddit/client-secret)" exec mcp-reddit'
  ```
  gopass may need to decrypt with GPG when the server starts. The MCP client cannot show a terminal prompt, so `gpg-agent` must already have the key cached or use a GUI pinentry. The README includes analogous one-liners for 1Password (`op run`) and the macOS Keychain (`security find-generic-password -w`).
- An equivalent Claude Desktop `claude_desktop_config.json` snippet.
