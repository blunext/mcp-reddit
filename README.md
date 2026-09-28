# mcp-reddit

A read-only [Model Context Protocol](https://modelcontextprotocol.io) server that lets AI assistants such as Claude Code and Claude Desktop research Reddit: search posts, read threads with their comments, expand collapsed branches, find other discussions of the same link, and discover subreddits.

It runs locally over stdio, uses Reddit's official OAuth API with your own credentials, never writes anything to Reddit and never stores Reddit data. See [PRIVACY.md](PRIVACY.md).

## Tools

| Tool | What it does |
|---|---|
| `search_posts` | Search posts by keywords, optionally in one subreddit; sort, time window, pagination |
| `get_post` | Read a post and its comment tree; accepts ids, URLs, `redd.it` and mobile share links |
| `expand_comments` | Load collapsed comment branches using tokens from `get_post` output |
| `get_related_posts` | Other submissions of the same link across subreddits |
| `search_subreddits` | Find subreddits by name or topic |

## Install

```sh
go install github.com/blunext/mcp-reddit/cmd/mcp-reddit@latest
```

## Credentials

The server needs Reddit API credentials (a client id and secret) for application-only OAuth access.

Since November 2025 Reddit no longer issues API keys self-service at reddit.com/prefs/apps. To get credentials:

1. Read Reddit's [Responsible Builder Policy](https://support.reddithelp.com/hc/en-us/articles/42728983564564-Responsible-Builder-Policy).
2. Submit a request through [Reddit Developer Support](https://support.reddithelp.com/hc/en-us/requests/new?ticket_form_id=14868593862164). Describe your use case, the data you need and your expected request volume, and link to a privacy policy.
3. Wait for manual review. It can take weeks, and requests can be rejected.

Unauthenticated access is not an option: Reddit has returned HTTP 403 to unauthenticated API requests since May 2026.

## Configuration

| Variable | Required | Description |
|---|---|---|
| `REDDIT_CLIENT_ID` | yes | OAuth client id |
| `REDDIT_CLIENT_SECRET` | yes | OAuth client secret |
| `REDDIT_USER_AGENT` | no | Custom User-Agent; defaults to `mcp-reddit/<version> (+https://github.com/blunext/mcp-reddit)` |

### Claude Code

Plain environment variables (the secret is stored in plain text in your Claude Code config):

```sh
claude mcp add reddit -e REDDIT_CLIENT_ID=your-id -e REDDIT_CLIENT_SECRET=your-secret -- mcp-reddit
```

Recommended: keep the secret in a password manager and fetch it when the server starts.

gopass:

```sh
claude mcp add reddit -- sh -c 'REDDIT_CLIENT_ID="$(gopass show -o reddit/client-id)" REDDIT_CLIENT_SECRET="$(gopass show -o reddit/client-secret)" exec mcp-reddit'
```

The MCP client starts the server without a terminal, so gopass cannot prompt for your GPG passphrase. Make sure `gpg-agent` already has the key unlocked, or configure a graphical pinentry.

1Password CLI:

```sh
claude mcp add reddit -e REDDIT_CLIENT_ID="op://Private/Reddit API/username" -e REDDIT_CLIENT_SECRET="op://Private/Reddit API/credential" -- op run -- mcp-reddit
```

macOS Keychain (store the secret first with `security add-generic-password -s mcp-reddit -a client-secret -w`):

```sh
claude mcp add reddit -e REDDIT_CLIENT_ID=your-id -- sh -c 'REDDIT_CLIENT_SECRET="$(security find-generic-password -s mcp-reddit -a client-secret -w)" exec mcp-reddit'
```

### Claude Desktop

In `claude_desktop_config.json`:

```json
{
  "mcpServers": {
    "reddit": {
      "command": "mcp-reddit",
      "env": {
        "REDDIT_CLIENT_ID": "your-id",
        "REDDIT_CLIENT_SECRET": "your-secret"
      }
    }
  }
}
```

The wrapper commands above also work here: set `"command": "sh"` and `"args": ["-c", "…"]`.

## Development

```sh
go test ./...                      # unit tests; live tests skip without credentials
go test ./internal/tools -update   # regenerate golden files after intended output changes
golangci-lint run
```

Live tests run against the real API when `REDDIT_CLIENT_ID` and `REDDIT_CLIENT_SECRET` are set. Set `REDDIT_TEST_SHARE_URL` to also test share-link resolution.

## License

[MIT](LICENSE)
