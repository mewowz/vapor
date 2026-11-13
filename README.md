# vapor

[![Go Reference](https://pkg.go.dev/badge/github.com/mewowz/vapor.svg)](https://pkg.go.dev/github.com/mewowz/vapor)

**vapor** is a Go library for talking to Steam: the CM network, game
coordinators, Steam Community, and the Steam Web API, without the Steam
desktop client.

It started as a fork of [go-steam](https://github.com/paralin/go-steam) and is
being rebuilt against the current Steam protocol, with three uses in mind:

- **A scriptable steamcmd replacement:** parallel depot downloads, JSON output,
  and something you can drive from a script.
- **Tooling for bot fleets:** many accounts, trade offers and confirmations,
  without one slow consumer stalling everything else.
- **Market and community data collection** for research, with sane rate limiting.

## Status

**Pre-1.0. Expect breaking changes.** Package names, types and function
signatures will change while the protocol layer is rebuilt. Pin a commit if
you depend on it.

Some go-steam features no longer work because Steam no longer supports them.
vapor removes them instead of shipping code that can't work:

| Removed | Why |
| --- | --- |
| Real-time trading (`trade/`, `Client.Trading`) | Steam no longer answers trade requests. Use trade offers instead. |
| WebAPI-nonce web login (`Client.Web`) | Steam no longer answers the nonce request after token logon. A token-based web session is planned. |

Both were checked against live Steam on 2026-11-07.

## Installation

```sh
go get github.com/mewowz/vapor
```

Use the Go version declared in [go.mod](./go.mod).

## Packages

| Package | Purpose |
| --- | --- |
| [`steam`](https://pkg.go.dev/github.com/mewowz/vapor) | Steam connections, authentication, friends, chat, presence, and GC transport |
| [`gsbot`](./gsbot) | Bot lifecycle helpers and an [example bot](./gsbot/gsbot) |
| [`web`](./web) | Context-aware Steam Web API client: profiles, applications, inventories, schemas, and trade offers |
| [`web/dota`](./web/dota) | Dota match history and match details over HTTP |
| [`steamid`](./steamid) | Steam account and group identifiers |
| [`tradeoffer`](./tradeoffer) | Steam Community trade offers |
| [`community`](./community) | Steam Community session cookies |
| [`economy/inventory`](./economy/inventory) | Steam Community inventories |
| [`tf2`](./tf2) | Team Fortress 2 game-coordinator operations |

The `web` packages use HTTP and an API key, separately from the CM connection.

## Steam Web API

```go
client, err := web.NewClient(web.Config{APIKey: apiKey})
if err != nil {
    return err
}

profiles, err := client.GetPlayerSummaries(ctx, []steamid.SteamId{accountID})
if err != nil {
    return err
}
```

Pass a context to every request. The default client uses a 30-second HTTP
timeout and a 64 MiB response limit. Callers handle caching and retries;
mutations are never retried automatically. See [the Web API guide](./web/README.md)
for pagination, error handling, and API changes.

## Protocol generation

Generated sources and their definitions are checked in. Normal builds do not
run generators. To regenerate from the retained definitions, run from the
repository root:

```sh
go mod vendor
go run ./generator steamlang proto
```

The Go SteamLanguage parser emits fixed-width binary messages and enums.
Protobuf messages use
[`protobuf-go-lite`](https://github.com/aperturerobotics/protobuf-go-lite)
for binary and JSON codecs.

Refreshing upstream definitions is a separate operation. See
[the generator guide](./generator/README.md) for where the definitions come
from and how to update them.

## Testing

```sh
go test -race -timeout=60s ./...
```

Web API tests use local HTTP servers and don't need credentials. Protocol
tests cover binary layouts, protobuf framing, malformed lengths, buffer reuse,
and reproducible generation.

## Credits and license

vapor is a fork of [go-steam](https://github.com/paralin/go-steam), originally
written by [Philipp Schröer](https://github.com/Philipp15b) and later
maintained by [Christian Stewart](https://github.com/paralin) and many
contributors. The transport, authentication and Web API code vapor builds on
is their work. Protocol knowledge throughout comes from
[SteamKit](https://github.com/SteamRE/SteamKit).

- The Go library is distributed under the [New BSD License](./LICENSE.txt).
- The Web API port keeps its [MIT notice](./web/LICENSE).
- Imported SteamLanguage definitions keep their
  [SteamRE LGPL notice](./generator/steamlang/LICENSE) and
  [provenance](./generator/steamlang/README.md).

vapor is not affiliated with or endorsed by Valve Corporation, or by the
go-steam or SteamKit authors. Steam is a trademark of Valve Corporation.
