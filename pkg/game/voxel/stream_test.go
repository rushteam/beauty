package voxel

import (
	"reflect"
	"testing"
)

func TestRLE_Roundtrip(t *testing.T) {
	blocks := []BlockID{0, 0, 0, 1, 1, 2, 0, 0, 0, 0}
	runs := EncodeRLE(blocks)

	want := []RLERun{
		{Block: 0, Count: 3},
		{Block: 1, Count: 2},
		{Block: 2, Count: 1},
		{Block: 0, Count: 4},
	}
	if !reflect.DeepEqual(runs, want) {
		t.Errorf("EncodeRLE = %v, want %v", runs, want)
	}

	decoded := DecodeRLE(runs)
	if !reflect.DeepEqual(decoded, blocks) {
		t.Errorf("DecodeRLE = %v, want %v", decoded, blocks)
	}
}

func TestRLE_ChunkRoundtrip(t *testing.T) {
	c := NewChunk(ChunkPos{})
	// 大部分是空气,只设几个方块
	c.Set(0, 0, 0, 1)
	c.Set(1, 0, 0, 1)
	c.Set(2, 0, 0, 2)

	runs := EncodeChunkRLE(c)
	// 应该压缩很好(大量连续 Air)
	if len(runs) > 10 {
		t.Errorf("RLE runs = %d, expected good compression", len(runs))
	}

	c2 := NewChunk(ChunkPos{1, 0, 0})
	if !DecodeChunkRLE(c2, runs) {
		t.Error("DecodeChunkRLE should return true")
	}
	if c2.Get(0, 0, 0) != 1 || c2.Get(1, 0, 0) != 1 || c2.Get(2, 0, 0) != 2 {
		t.Error("DecodeChunkRLE data mismatch")
	}
	if c2.Get(3, 0, 0) != Air {
		t.Error("unset block should be Air")
	}
}

func TestRLE_AllSame(t *testing.T) {
	blocks := make([]BlockID, 1000)
	runs := EncodeRLE(blocks)
	if len(runs) != 1 {
		t.Errorf("all-same runs = %d, want 1", len(runs))
	}
	if runs[0].Count != 1000 {
		t.Errorf("count = %d, want 1000", runs[0].Count)
	}
}

func TestRLE_Empty(t *testing.T) {
	if EncodeRLE(nil) != nil {
		t.Error("EncodeRLE(nil) should return nil")
	}
	if DecodeRLE(nil) != nil {
		t.Error("DecodeRLE(nil) should return nil")
	}
}

func TestMarshalUnmarshalRLE(t *testing.T) {
	runs := []RLERun{
		{Block: 0, Count: 100},
		{Block: 42, Count: 5},
		{Block: 0, Count: 200},
	}
	data := MarshalRLE(runs)
	if len(data) != 12 { // 3 runs * 4 bytes
		t.Errorf("MarshalRLE len = %d, want 12", len(data))
	}
	got := UnmarshalRLE(data)
	if !reflect.DeepEqual(got, runs) {
		t.Errorf("UnmarshalRLE = %v, want %v", got, runs)
	}

	// 错误长度
	if UnmarshalRLE([]byte{1, 2, 3}) != nil {
		t.Error("UnmarshalRLE bad length should return nil")
	}
}

func TestDeltaFromChanges(t *testing.T) {
	cp := ChunkPos{0, 0, 0}
	changes := []BlockChange{
		{Pos: BlockPos{0, 0, 0}, OldBlock: Air, NewBlock: 1},
		{Pos: BlockPos{16, 0, 0}, OldBlock: Air, NewBlock: 2}, // 不同 chunk
		{Pos: BlockPos{5, 5, 5}, OldBlock: Air, NewBlock: 3},
	}

	delta := DeltaFromChanges(cp, changes)
	if delta == nil {
		t.Fatal("delta should not be nil")
	}
	if len(delta.Changes) != 2 {
		t.Errorf("delta changes = %d, want 2", len(delta.Changes))
	}

	// 不同 chunk 无匹配
	delta2 := DeltaFromChanges(ChunkPos{5, 5, 5}, changes)
	if delta2 != nil {
		t.Error("no matching changes should return nil")
	}
}

func TestApplyDelta(t *testing.T) {
	c := NewChunk(ChunkPos{0, 0, 0})
	delta := &ChunkDelta{
		Pos: ChunkPos{0, 0, 0},
		Changes: []BlockEntry{
			{Offset: uint16(index(3, 4, 5)), Block: 99},
		},
	}
	ApplyDelta(c, delta)
	if c.Get(3, 4, 5) != 99 {
		t.Error("ApplyDelta should set block")
	}

	// nil delta
	ApplyDelta(c, nil) // 不崩溃即可
}

func BenchmarkRLE_EncodeChunk(b *testing.B) {
	c := NewChunk(ChunkPos{})
	// 模拟地形:底部石头,中间空气
	for x := 0; x < ChunkSize; x++ {
		for z := 0; z < ChunkSize; z++ {
			for y := 0; y < 8; y++ {
				c.Set(x, y, z, 1) // 石头
			}
		}
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		EncodeChunkRLE(c)
	}
}
