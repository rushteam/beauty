# P2P DataChannel Signaling and Networking

Beauty's P2P capability turns WebRTC from a "media call tool" into a **general-purpose P2P data pipe** —
peers establish DataChannel connections directly; data does not pass through the server, yielding lower latency and bandwidth cost.

Building on Beauty's existing WebRTC (pion), WebSocket (`pkg/ws`), QUIC (`pkg/quic`), and
presence (`pkg/presence`), this layer adds **signaling orchestration + peer discovery + topology strategies + dual-channel abstraction**.

## Package Layout

```
pkg/p2p/
├── p2p.go           # PeerConn / Message / Network / Transport core interfaces
├── network.go       # LocalNetwork: in-memory impl managing multiple PeerConns
├── topology/
│   └── topology.go  # Topology interface + FullMesh / Star / MatchPairs / Queue
└── signaling/
    └── signaling.go # WebSocket signaling server (rooms, peer discovery, signal relay)
```

## Architecture Overview

```
┌─────────────────────────────────────────────────────────────────┐
│                          Application Layer                       │
│  (game state sync / collaborative editing / file transfer /     │
│   P2P chat)                                                      │
├────────────────────────────────┬────────────────────────────────┤
│         PeerConn interface     │        Network interface        │
│  SendReliable / SendUnreliable │  Peers / Broadcast / Events    │
├────────────────────────────────┴────────────────────────────────┤
│                       Transport Layer (pluggable)                │
│  ┌──────────────────┐  ┌──────────────────┐                    │
│  │ WebRTC DataChannel│  │  QUIC(pkg/quic) │                    │
│  │  (browser-ready)  │  │  (native/server) │                    │
│  └──────────────────┘  └──────────────────┘                    │
├─────────────────────────────────────────────────────────────────┤
│                     Signaling & Topology Layer                   │
│  pkg/p2p/signaling      pkg/p2p/topology                        │
│  (WS signal relay)      (FullMesh / Star / MatchPairs)          │
└─────────────────────────────────────────────────────────────────┘
```

## Core Interfaces

### PeerConn — Dual-Channel Peer Connection

```go
type PeerConn interface {
    ID() PeerID
    SendReliable(data []byte) error    // reliable ordered (TCP-like)
    SendUnreliable(data []byte) error  // unreliable unordered (UDP-like)
    Recv() <-chan Message
    Context() context.Context
    Close() error
}
```

Why dual channels:
- **Reliable channel**: chat messages, RPC, game events — must not drop, must preserve order
- **Unreliable channel**: position sync, input frames, state snapshots — drops are fine, want latest value

### Network — P2P Network View

```go
type Network interface {
    LocalID() PeerID
    Peers() []PeerConn
    Peer(id PeerID) PeerConn
    Events() <-chan PeerEvent      // connect/disconnect events
    Broadcast(data []byte, reliable bool) error
    Close() error
}
```

### Topology — Topology Strategy

```go
type Topology interface {
    OnPeerJoin(newPeer PeerID, existingPeers []PeerID) []PeerPair
    OnPeerLeave(leavingPeer PeerID, remainingPeers []PeerID) []PeerPair
}
```

Built-in strategies:

| Strategy | Connection Count | Use Case |
|---|---|---|
| `FullMesh` | n*(n-1)/2 | Small groups ≤8 (game battles, collaboration) |
| `Star{Hub}` | n-1 | Host/client mode (room owner hosts) |
| `MatchPairs` / `Queue` | n/2 | 1v1 matchmaking |

## Signaling Protocol

Signaling runs over WebSocket with a JSON envelope format:

```json
{"event": "join", "data": {"room": "game-1", "peer_id": "alice"}}
{"event": "relay", "data": {"to": "bob", "data": "<offer/answer/candidate>"}}
```

Full flow:

```
Client A                    Server                    Client B
   │─── join(room) ──────────→│                          │
   │←── assign_id(A) ─────────│                          │
   │                           │←── join(room) ──────────│
   │                           │──→ assign_id(B) ────────→│
   │←── peer_joined(B,init) ──│──→ peer_joined(A,resp) ─→│
   │                           │                          │
   │─── relay(to:B, offer) ──→│──→ signal(from:A) ──────→│
   │←── signal(from:B) ───────│←── relay(to:A, answer) ──│
   │─── relay(to:B, cand) ───→│──→ signal(from:A) ──────→│
   │←── signal(from:B) ───────│←── relay(to:A, cand) ────│
   │                           │                          │
   │═══════════ DataChannel direct (not via server) ══════│
```

## Relationship to Existing Packages

| Package | Role |
|---|---|
| `pkg/ws` | WebSocket connection carrying signaling |
| `pkg/presence` | Optional — cross-node peer discovery (distributed deployment) |
| `pkg/media/webrtc` | Shared pion dependency; P2P uses DataChannel, not media tracks |
| `pkg/media/webrtc/sfu` | Contrast: SFU is peer↔server; P2P is peer↔peer |
| `pkg/quic` | Optional — reliable/unreliable dual-channel alternative for native clients |
| `pkg/gameloop` | Optional — attach to PeerConn for fixed tick-rate game sync |

## Usage

### Server (3 lines)

```go
sig := signaling.NewServer(
    signaling.WithTopology(topology.FullMesh{}),
    signaling.WithWSOptions(ws.WithOriginPatterns("*")),
)
mux.Handle("/ws", sig.Handler())
```

### Client (Browser JS)

1. WebSocket to `/ws`, send `join`
2. Receive `peer_joined(initiator=true)` → create `RTCPeerConnection` + DataChannel, send offer
3. Receive `signal(offer)` → create answer
4. After connection is established, send/receive data via DataChannel

Full frontend code: [examples/p2p-signaling](../examples/p2p-signaling).

## Extension Directions

### Small-Scale P2P + Large-Scale SFU Fallback

Beauty has both P2P and SFU, enabling **automatic fallback**:

```
≤4 people  → FullMesh P2P (lowest latency, zero server bandwidth)
5~50 people → SFU relay (pkg/media/webrtc/sfu)
50+ people → cascaded SFU / CDN distribution (pkg/hls)
```

### Distributed Signaling

A single-node `signaling.Server` can scale to multiple nodes via `pkg/presence` + `pkg/router`:
- presence tracks "which signaling node a peer is on"
- router forwards relay messages across nodes
- each node still uses a local `signaling.Server` for its own peers

### QUIC Transport Backend

Native clients (non-browser) can skip WebRTC and use `pkg/quic` directly:
- Stream = reliable channel
- Datagram = unreliable channel
- Implement the `p2p.PeerConn` interface to plug into the upper layer seamlessly
