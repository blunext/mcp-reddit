# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project

`mcp-reddit` is an open-source, **read-only** Model Context Protocol (MCP) server for Reddit, written in Go (module `github.com/blunext/mcp-reddit`, local toolchain Go 1.27). Users run it locally over stdio so an AI assistant can research topics on Reddit and analyze specific threads.

The design is in `docs/superpowers/specs/2026-09-28-reddit-mcp-design.md`. Read it before changing tools, the Reddit client or auth. No code exists yet; implementation follows the stage 1 plan.

## Commands

Standard Go tooling, once `go.mod` exists:

```sh
go build ./...                          # build everything
go test ./...                           # run all tests
go test ./path/to/pkg -run TestName -v  # run a single test
go test ./internal/tools -update        # regenerate golden files
go vet ./...
golangci-lint run
```

Integration tests hit the live Reddit API and run only when `REDDIT_CLIENT_ID` and `REDDIT_CLIENT_SECRET` are set; otherwise they skip.

## Architecture

- `cmd/mcp-reddit`: reads env config, wires everything, and serves over stdio via the official `github.com/modelcontextprotocol/go-sdk`. stdout is the MCP protocol channel, so log to stderr only.
- `internal/reddit`: thin `net/http` client for Reddit's OAuth API. It owns all Reddit wire-format quirks (`Listing`/`Thing`, `replies: ""`, `more` objects, `morechildren` reassembly), token handling and rate limiting, and exposes domain types only. It knows nothing about MCP.
- `internal/tools`: MCP tool handlers and compact plain-text rendering. They depend on the Reddit client through an interface, so tests use a fake client and golden files.

## Hard constraints

- **OAuth app-only (`client_credentials`) is the only auth mode.** Reddit returns 403 for unauthenticated `.json` requests (since May 2026), so don't add an anonymous or RSS fallback.
- **Read-only.** No write endpoints, no user login.
- **No persistence.** Never write Reddit data to disk. `PRIVACY.md` promises this under Reddit's Responsible Builder Policy (48-hour deletion, no model training), so keep the code and that file in sync.
- **Tool errors, not protocol errors.** Reddit failures are returned as tool results with `IsError: true` and messages written for the model.
- **JSON: use `encoding/json/v2` and `encoding/json/jsontext`, never `encoding/json` v1.** Custom decoders implement `UnmarshalJSONFrom`.
- **Tool calls must not block for long.** On an exhausted rate limit, wait only if the reset is ≤ 5 s away; otherwise return an error.
