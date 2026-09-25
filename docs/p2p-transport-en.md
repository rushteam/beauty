# P2P Transport Implementations

> 中文版: [p2p-transport.md](p2p-transport.md)

`pkg/transport/p2p` defines transport-agnostic P2P interfaces (`Transport`, `PeerConn`, `Network`),
and the following three implementations cover different scenarios:

## Selection Guide

| Transport | Best for | Reliable channel | Unreliable channel | NAT traversal | External deps |
|-----------|---------|---------|-----------|---------|---------|
| **TCP** | Intranet/LAN, service-to-service communication | TCP ✅ | Falls back to TCP (still reliable) | ❌ Not supported | None (stdlib) |
| **QUIC** | Cross-datacenter/edge nodes, game servers | QUIC Stream ✅ | QUIC Datagram ✅ | Partial (UDP) | quic-go |
| **WebRTC** | Browser interconnect, traversal across NAT | DataChannel (ordered) ✅ | DataChannel (unordered) ✅ | ✅ ICE/STUN/TURN | pion/webrtc |

## 1. TCP Transport (`pkg/transport/p2p/tcptransport`)

**Best for:**
- Intranet / same-datacenter P2P communication between Go services (no NAT)
- LAN multiplayer (game LAN parties, IoT device interconnect)
- Getting through corporate firewalls (TCP 443 is not blocked)
- High reliability requirements, not latency-sensitive

**Characteristics:**
- Zero external dependencies, uses only the `net` standard library
- `SendUnreliable` falls back to a TCP send (still reliable and ordered)
- Both sides must be network-reachable (no NAT traversal)

```go
import "github.com/rushteam/beauty/pkg/transport/p2p/tcptransport"

// create the transport
t, _ := tcptransport.New("node-1", ":9000",
    tcptransport.WithAddressBook(map[string]string{
        "node-2": "192.168.1.20:9000",
    }),
)
defer t.Close()

// connect
conn, _ := t.Connect(ctx, "node-2")
conn.SendReliable([]byte("hello"))

// or accept an inbound connection
conn, _ = t.Accept(ctx)
```

## 2. QUIC Transport (`pkg/transport/p2p/quictransport`)

**Best for:**
- High-performance server-to-server P2P communication (cross-datacenter, edge nodes)
- State synchronization between game servers (true dual channels)
- Fast 0-RTT reconnects
- Native mobile apps (connection migration: seamless WiFi↔4G switching)

**Characteristics:**
- Reliable channel: QUIC Stream (multiplexed, no cross-stream head-of-line blocking)
- Unreliable channel: QUIC Datagram (RFC 9221, true UDP semantics — no retransmission)
- Built-in TLS 1.3 (encryption is mandatory)
- Better NAT traversal than TCP (UDP hole punching is easier than TCP)

```go
import (
    "github.com/rushteam/beauty/pkg/transport/p2p/quictransport"
    "github.com/rushteam/beauty/pkg/transport/quic"
)

t, _ := quictransport.New("node-1", ":8443",
    []quic.Option{quic.WithTLSConfig(tlsConf)},
    quictransport.WithAddressBook(map[string]string{
        "node-2": "10.0.1.5:8443",
    }),
    quictransport.WithDialOptions(quic.WithClientTLSConfig(clientTLS)),
)
defer t.Close()

conn, _ := t.Connect(ctx, "node-2")
conn.SendReliable([]byte("critical event"))   // QUIC Stream
conn.SendUnreliable([]byte("position update")) // QUIC Datagram (may be dropped)
```

## 3. WebRTC Transport (`contrib/p2p-webrtc`)

**Best for:**
- P2P communication between browsers and Go services (the only option)
- Go processes that need to traverse complex NATs (automatic ICE traversal)
- Interop with the frontend JS client (`p2p-client.js`)
- Direct connections between game clients (Web/Electron) and matchmaking servers

**Characteristics:**
- Automatic NAT traversal via the ICE framework (STUN + TURN fallback)
- Fully compatible with the browser's RTCPeerConnection
- Requires a signaling service (`pkg/transport/p2p/signaling`)
- Reliable channel: ordered DataChannel
- Unreliable channel: unordered DataChannel (maxRetransmits=0)

```go
import webrtc "github.com/rushteam/beauty/contrib/p2p-webrtc"

// signalFunc is provided by the signaling client (sends to the peer over WebSocket)
t := webrtc.New("player-1", signalFunc,
    webrtc.WithICEServers([]webrtc.ICEServer{
        {URLs: []string{"stun:stun.l.google.com:19302"}},
        {URLs: []string{"turn:my-turn.example.com"}, Username: "u", Credential: "p"},
    }),
)
defer t.Close()

// called when the signaling client receives a message from the peer:
t.HandleSignal(ctx, "player-2", signalMsg)

// connect actively (send the offer)
conn, _ := t.Connect(ctx, "player-2")
conn.SendReliable([]byte("game event"))
conn.SendUnreliable([]byte("input frame"))

// or accept a connection (the peer sends the offer)
conn, _ = t.Accept(ctx)
```

## Architecture Diagram

```
                        ┌─────────────────┐
                        │   pkg/transport/p2p       │ ← interface layer (Transport, PeerConn, Network)
                        │   p2p.go        │
                        └────────┬────────┘
                                 │
              ┌──────────────────┼──────────────────┐
              │                  │                  │
    ┌─────────▼─────────┐ ┌─────▼─────────┐ ┌─────▼───────────────┐
    │ pkg/transport/p2p/          │ │ pkg/transport/p2p/      │ │ contrib/p2p-webrtc  │
    │ tcptransport      │ │ quictransport │ │                     │
    │                   │ │               │ │ (pion/webrtc v4)    │
    │ · net.Conn        │ │ · pkg/transport/quic    │ │ · ICE/DTLS/SCTP     │
    │ · zero deps       │ │ · quic-go     │ │ · DataChannel       │
    └───────────────────┘ └───────────────┘ └─────────────────────┘
         Intranet/LAN       Cross-DC/edge      Browser/NAT traversal
```

## How to Choose

1. **Both sides in the same intranet/datacenter?** → TCP (simplest, zero configuration)
2. **Need a true unreliable channel (game position sync)?** → QUIC or WebRTC
3. **One side is a browser?** → WebRTC (the only choice)
4. **Need NAT traversal without a browser involved?** → WebRTC (strongest via ICE) or QUIC (if you control the network)
5. **Need encryption?** → QUIC (built-in TLS) or WebRTC (built-in DTLS)
6. **Chasing the lowest latency?** → QUIC Datagram or WebRTC unordered DataChannel
