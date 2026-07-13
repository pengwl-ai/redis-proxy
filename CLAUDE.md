# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

A Redis proxy service written in Go that implements the Redis Serialization Protocol (RESP) to transparently forward client requests to Redis backends. The proxy supports primary/standby Redis datacenter configuration with hot-swappable roles via an HTTP API, enabling failover without application code changes.

## Architecture

```
Client (RESP) ──► Proxy ──► Primary Redis
                      │
                      └──► Standby Redis(es)
```

- The proxy speaks RESP to clients and forwards requests to backend Redis instances
- Primary Redis: write/read traffic; failures on primary are terminal
- Standby Redis: failures on standby are tolerated and do not affect the client
- An HTTP API (Gin framework) allows runtime modification of primary/standby assignments
- Redis authentication is handled at the proxy layer — credentials are parsed from the client handshake and forwarded

## Tech Stack

- **Language**: Go
- **HTTP framework**: Gin (for the management API)
- **Protocol**: RESP2/RESP3 (see `docs/protocol-spec.md` for the full specification)

## Implementation Phases

1. RESP protocol parser and basic proxy (GET, SET, DEL commands)
2. Redis authentication forwarding
3. Multi-instance routing with primary/standby roles
4. Management API for role switching
