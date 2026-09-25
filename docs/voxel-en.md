# Voxel World Primitives (pkg/game/voxel)

> 中文版: [voxel.md](voxel.md)

beauty adds a package of server-side voxel world primitives under `pkg/game/`, covering the core
server-side needs of Minecraft-style voxel games: chunk storage, block mutation tracking, incremental sync, RLE-compressed transfer, world snapshots and restore, and 3D spatial indexing.

It follows the beauty style: mechanisms not tied to policy, pure standard library, generics, and concurrency safety.

## Component overview

| Component | In one sentence | Typical scenarios |
|------|--------|----------|
| `Chunk` | 16³ block array, O(1) reads/writes | Chunk load/unload, block access |
| `World` | Chunk manager | Cross-chunk coordinate conversion, range queries |
| `Mutation` + `MutationLog` | Mutation tracking + ring log | Incremental sync, catch-up after reconnect |
| `EncodeRLE` / `DecodeRLE` | Run-length compression | Chunk network transfer (compression ratio up to 99%) |
| `ChunkDelta` / `ApplyDelta` | Block-level delta | Per-frame incremental sync (send only changed blocks) |
| `WorldSnapshot` / `SnapshotRing` | World snapshot + ring buffer | Persistence, crash recovery |
| `octree.Tree` | Octree 3D spatial index | Entity AOI in a voxel world |

## Architecture diagram

```
        Client  ←── ws/quic ──→  Server
          │                      │
          │  ┌───────────────────┤
          │  │                   │
          ▼  ▼                   ▼
     RLE decode            World + Mutation
     ApplyDelta               │
     octree(client entities) ┌───┴───┐
                          │       │
                       Chunk   Mutation
                       storage  tracking
                          │       │
                          ▼       ▼
                    ┌─────────────────┐
                    │ Incremental sync│
                    │  RLE (full)     │
                    │  Delta (incr.)  │
                    │  CatchUp        │
                    └─────────────────┘
                          │
                    Snapshot (persistence)
```

## Cheat sheet: core types

### Coordinate system

```go
// absolute block coordinates in the world (integers)
pos := voxel.BlockPos{X: 5, Y: 64, Z: -17}

// convert to chunk coordinates
cp := pos.ToChunkPos()     // ChunkPos{0, 4, -2}

// convert to local coordinates within the chunk (0~15)
lx, ly, lz := pos.ToLocal() // (5, 0, 15)

// negative coordinates are handled correctly (-17 → chunk -2, local 15)
voxel.BlockPos{-17, 0, 0}.ToChunkPos() // ChunkPos{-2, 0, 0}

// neighbor queries
cp.Neighbors6()  // 6 orthogonal neighbors (±X, ±Y, ±Z)
cp.Neighbors26() // 26 neighbors (including diagonals)
```

### Chunk (chunk storage)

```go
c := voxel.NewChunk(voxel.ChunkPos{0, 0, 0})

c.Get(5, 10, 3)          // read local coordinates; out of range returns Air
c.Set(5, 10, 3, 42)      // write, returns the old value, increments Revision
c.Fill(Stone)             // fill the entire chunk
c.IsEmpty()               // whether it is all Air
c.NonAirCount()           // number of non-air blocks
c.Revision()              // revision number (incremented on each Set)
c.Blocks()                // copy of the underlying array (for serialization)
c.LoadBlocks(data)        // restore from external data (deserialization)
c.ForEach(func(x, y, z int, block voxel.BlockID) {
    // iterate over all non-Air blocks
})
```

- Internally uses a flat array `[4096]uint16` (16³), cache-friendly
- Concurrency-safe (read-write lock)
- Writing the same value does not increment Revision

### World (world management)

```go
w := voxel.NewWorld()

// chunk management
c, created := w.LoadChunk(pos)   // load/create a chunk
w.UnloadChunk(pos)               // unload (returns the Chunk for persistence)
w.Chunk(pos)                     // lookup (returns nil if absent)
w.HasChunk(pos)                  // whether it is loaded
w.ChunkCount()                   // number of loaded chunks

// cross-chunk block reads/writes (coordinate conversion handled automatically)
w.GetBlock(voxel.BlockPos{-1, 64, 33})    // returns Air if not loaded
w.SetBlock(voxel.BlockPos{-1, 64, 33}, 1) // creates the chunk automatically if not loaded

// range queries
w.ChunksInRadius(center, 3)      // loaded chunks within radius 3 (Chebyshev distance)
w.ForEachChunk(func(pos, c) {})  // iterate over all loaded chunks
w.LoadedChunks()                 // snapshot of all loaded chunk coordinates
```

## Cheat sheet: mutation tracking + incremental sync

### Mutation (mutation tracking)

```go
m := voxel.NewMutation()

// option 1: record manually
w.SetBlock(pos, block)
m.Record(voxel.BlockChange{Pos: pos, OldBlock: old, NewBlock: block})

// option 2: do it in one step (recommended)
m.RecordSet(w, pos, block) // SetBlock + automatic Record

// commit at the end of each tick
cm := m.Commit() // takes this frame's changes and increments Revision; returns nil if no changes
```

### MutationLog (catch-up after reconnect)

```go
log := voxel.NewMutationLog(128) // keep the most recent 128 frames

// append on each tick
if cm := m.Commit(); cm != nil {
    log.Append(*cm)
}

// client reconnect: catch up on all changes with revision > lastKnown
changes, ok := log.Since(lastKnown)
if !ok {
    // too old, a full sync is required
}
```

### ChunkDelta (block-level delta)

```go
// extract the delta for a given Chunk from a CommittedMutation
delta := voxel.DeltaFromChanges(chunkPos, cm.Changes)

// apply the delta on the client
voxel.ApplyDelta(clientChunk, delta)
```

## Cheat sheet: RLE-compressed transfer

Voxel worlds contain long runs of identical blocks (air layers, stone layers), so RLE compresses extremely well.

```go
// compress
runs := voxel.EncodeChunkRLE(chunk)
data := voxel.MarshalRLE(runs) // compact binary (4 bytes per run)

// decode
runs = voxel.UnmarshalRLE(data)
voxel.DecodeChunkRLE(targetChunk, runs)
```

**Reference compression results** (16³ chunk, 8192 bytes raw):

| Terrain type | Runs | Compressed bytes | Compression ratio |
|----------|--------|-----------|--------|
| Pure air | 1 | 4 | 0.05% |
| Half stone, half air | 2~32 | 8~128 | 0.1%~1.6% |
| Typical surface (stone/dirt/grass/air) | 8~64 | 32~256 | 0.4%~3% |
| Fully random | ~4096 | ~16384 | 200% (not applicable) |

## Cheat sheet: world snapshots

```go
// take a consistent snapshot
snap := voxel.TakeSnapshot(w, mutation)

// restore (clears existing chunks, loads the chunks from the snapshot)
voxel.RestoreSnapshot(w2, snap)

// snapshot ring buffer (keeps the most recent N)
ring := voxel.NewSnapshotRing(8)
ring.Push(snap)
latest, ok := ring.Latest()
snap, rev, ok := ring.Nearest(revision)  // nearest snapshot <= revision
```

## Cheat sheet: octree (3D spatial index)

The 3D counterpart of `quadtree`, used for AOI of entities (players, mobs, dropped items) in a voxel world.

```go
tree := octree.New[string](
    octree.Box{X: 0, Y: 0, Z: 0, W: 1024, H: 256, D: 1024}, // world bounds
    16,  // node capacity (8~16 recommended)
    10,  // max depth
)

tree.Add("player1", 50, 64, 50)
tree.Move("player1", 55, 64, 48)
tree.Remove("zombie1")

// spherical range query (sorted by distance)
nearby := tree.Nearby(50, 64, 50, 32, "player1") // exclude self

// K nearest neighbors
knn := tree.KNN(50, 64, 50, 5, 100) // nearest 5, search radius 100

// bounding box query
entities := tree.QueryBox(octree.Box{X: 40, Y: 60, Z: 40, W: 20, H: 10, D: 20})

tree.Pos("player1") // look up coordinates
tree.Len()           // total number of entities
```

- Recursively subdivides crowded regions and merges sparse ones, adapting to density automatically
- Concurrency-safe (single read-write lock)
- Complements `spatial` (2D) / `quadtree` (2D), targeting scenarios with a height dimension

## Combining with existing game packages

### gameloop + voxel (modify the world within the authoritative tick)

```go
type VoxelHandler struct {
    world    *voxel.World
    mutation *voxel.Mutation
    log      *voxel.MutationLog
}

func (h *VoxelHandler) OnTick(frame uint64, inputs []PlayerInput) []Output {
    for _, in := range inputs {
        switch in.Action {
        case "dig":
            h.mutation.RecordSet(h.world, in.Pos, voxel.Air)
        case "place":
            h.mutation.RecordSet(h.world, in.Pos, in.Block)
        }
    }
    cm := h.mutation.Commit()
    if cm != nil {
        h.log.Append(*cm)
        return []Output{{Type: "delta", Data: cm}}
    }
    return nil
}
```

### ws/quic + RLE (chunk transfer)

```go
// player enters view range → send the full chunk (RLE-compressed)
runs := voxel.EncodeChunkRLE(chunk)
data := voxel.MarshalRLE(runs)
conn.Send(data) // 8KB raw → usually < 100 bytes

// every tick → send the delta
delta := voxel.DeltaFromChanges(chunkPos, cm.Changes)
conn.SendJSON(delta) // contains only the changed blocks
```

### octree + replicate (entity state sync)

```go
// the octree manages entity positions
tree.Move(entityID, x, y, z)

// combine with replicate for AOI
nearby3D := tree.Nearby(viewer.X, viewer.Y, viewer.Z, viewRadius)
// convert to spatial.Entity format, then feed to the Projector
delta := projector.Project(frame, viewer, visible, dirty, removed, lookup)
```

### snapbuf + snapshot (world snapshot rewind)

```go
// save a snapshot every N ticks (for rollback/debugging)
if frame%100 == 0 {
    snap := voxel.TakeSnapshot(world, mutation)
    snapshotRing.Push(snap)
}

// roll back to a given revision
if snap, ok := snapshotRing.At(targetRevision); ok {
    voxel.RestoreSnapshot(world, snap)
}
```

## Quick guide to complementary packages

| Need | Use this | Instead of | Because |
|------|--------|--------|------|
| 2D entities on a flat map | `spatial` / `quadtree` | `octree` | The former has no Z-axis overhead |
| 3D entities in a voxel world | `octree` | `spatial` | The latter has no height dimension |
| 2D map tile storage | Business-defined 2D array | `voxel.Chunk` | Chunk is 3D |
| 3D block storage | `voxel.Chunk` / `World` | Hand-written map | Chunk's flat array is more efficient |
| Incremental entity state sync | `replicate` | `voxel.Mutation` | replicate handles entities; Mutation handles blocks |
| Incremental block change sync | `voxel.Mutation` + `ChunkDelta` | `replicate` | Block changes are a voxel-specific problem |
| World snapshot rewind | `voxel.SnapshotRing` | `snapbuf.Ring` | The former stores the full world; the latter stores single-frame state |

## Performance reference

| Operation | Time | Notes |
|------|------|------|
| `Chunk.Get/Set` | ~10ns | Flat array indexing + read-write lock |
| `RLE encoding` (half-filled chunk) | ~15µs | Measured by benchmark |
| `Size after RLE compression` (typical terrain) | 32~256 bytes | 8192 bytes raw |
| `octree.Nearby` (10k entities, r=50) | ~20µs | Similar to the quadtree benchmark |
| `World.SetBlock` (cross-chunk) | ~50ns | Includes coordinate conversion + Chunk lookup |

## demo

`examples/voxel/main.go`, a single file you can `go run` directly, demonstrating the full workflow:
terrain generation → block operations → RLE compression → incremental sync → reconnect catch-up → snapshot restore → octree entity management.
