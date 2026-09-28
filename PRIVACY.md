# Privacy Policy

_Last updated: 2026-09-28_

`mcp-reddit` is an open-source, read-only [Model Context Protocol](https://modelcontextprotocol.io) server that lets an AI assistant running on a user's own computer read public Reddit content on that user's request. This policy describes what data the software handles and what it does with it.

## Who operates the software

There is no hosted service. Each user downloads the software and runs it locally on their own machine, using Reddit API credentials issued to them by Reddit. The maintainers of this project do not operate servers for it and never receive any data from it.

## What data is accessed

Only public Reddit content, retrieved through Reddit's official OAuth Data API using application-only (read-only) access:

- posts and their metadata (title, body, author username, subreddit, score, comment count, timestamps, links),
- comments on those posts,
- public subreddit information returned by subreddit search.

The software does not log in as a Reddit user and cannot access private messages, votes, account settings, or any non-public data. It cannot post, comment, vote, or perform any other write action.

## How data is used

Content is fetched only when the user's AI assistant calls one of the server's tools in response to a request from the user (for example "find opinions about X" or "summarize this thread"). The retrieved content is returned to that assistant so it can answer the user.

The AI assistant (MCP client) and its model provider are chosen and configured by the user. Content passed to them is subject to that provider's own terms and privacy policy, not this one.

## Storage and retention

- Reddit content is held **in memory only**, for the duration of the request that fetched it. The software may keep a short-lived in-memory cache (minutes, never beyond the lifetime of the process) to avoid repeated identical requests.
- Nothing is written to disk, to a database, or to any remote location by this software.
- All data is discarded when the process exits. Nothing is retained for anywhere near Reddit's recommended 48-hour limit, and content deleted on Reddit is never kept.

## What the software does not do

- It does not use Reddit data to train, fine-tune, or evaluate machine-learning or AI models.
- It does not sell, license, share, or commercialize Reddit data.
- It does not build profiles of Reddit users or track them across requests.
- It does not collect analytics or telemetry about its users.

## Credentials

The Reddit client ID and secret are supplied by the user through environment variables on their own machine. They are sent only to Reddit's OAuth endpoints and are never stored by the software or transmitted anywhere else.

## Changes

Changes to this policy are published in this file in the project repository, with the date above updated.

## Contact

Questions about this policy: open an issue in the project repository.
