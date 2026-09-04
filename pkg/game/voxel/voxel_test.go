package voxel

import "testing"

func TestBlockPos_ToChunkPos(t *testing.T) {
	tests := []struct {
		bp   BlockPos
		want ChunkPos
	}{
		{BlockPos{0, 0, 0}, ChunkPos{0, 0, 0}},
		{BlockPos{15, 15, 15}, ChunkPos{0, 0, 0}},
		{BlockPos{16, 0, 0}, ChunkPos{1, 0, 0}},
		{BlockPos{-1, 0, 0}, ChunkPos{-1, 0, 0}},
		{BlockPos{-16, 0, 0}, ChunkPos{-1, 0, 0}},
		{BlockPos{-17, 0, 0}, ChunkPos{-2, 0, 0}},
		{BlockPos{31, -32, 48}, ChunkPos{1, -2, 3}},
	}
	for _, tt := range tests {
		got := tt.bp.ToChunkPos()
		if got != tt.want {
			t.Errorf("BlockPos%v.ToChunkPos() = %v, want %v", tt.bp, got, tt.want)
		}
	}
}

func TestBlockPos_ToLocal(t *testing.T) {
	tests := []struct {
		bp         BlockPos
		wX, wY, wZ int
	}{
		{BlockPos{0, 0, 0}, 0, 0, 0},
		{BlockPos{5, 10, 15}, 5, 10, 15},
		{BlockPos{16, 0, 0}, 0, 0, 0},
		{BlockPos{-1, 0, 0}, 15, 0, 0},
		{BlockPos{-16, 0, 0}, 0, 0, 0},
		{BlockPos{-17, 0, 0}, 15, 0, 0},
	}
	for _, tt := range tests {
		gx, gy, gz := tt.bp.ToLocal()
		if gx != tt.wX || gy != tt.wY || gz != tt.wZ {
			t.Errorf("BlockPos%v.ToLocal() = (%d,%d,%d), want (%d,%d,%d)",
				tt.bp, gx, gy, gz, tt.wX, tt.wY, tt.wZ)
		}
	}
}

func TestChunkPos_Neighbors6(t *testing.T) {
	n := ChunkPos{0, 0, 0}.Neighbors6()
	if len(n) != 6 {
		t.Fatalf("Neighbors6 len = %d, want 6", len(n))
	}
	seen := make(map[ChunkPos]bool)
	for _, p := range n {
		seen[p] = true
	}
	for _, want := range []ChunkPos{
		{-1, 0, 0}, {1, 0, 0}, {0, -1, 0}, {0, 1, 0}, {0, 0, -1}, {0, 0, 1},
	} {
		if !seen[want] {
			t.Errorf("Neighbors6 missing %v", want)
		}
	}
}

func TestChunkPos_Neighbors26(t *testing.T) {
	n := ChunkPos{0, 0, 0}.Neighbors26()
	if len(n) != 26 {
		t.Fatalf("Neighbors26 len = %d, want 26", len(n))
	}
	for _, p := range n {
		if p.X == 0 && p.Y == 0 && p.Z == 0 {
			t.Error("Neighbors26 contains origin")
		}
	}
}

func TestFloorDiv(t *testing.T) {
	tests := []struct {
		a, b int32
		want int32
	}{
		{7, 16, 0},
		{16, 16, 1},
		{-1, 16, -1},
		{-16, 16, -1},
		{-17, 16, -2},
		{0, 16, 0},
	}
	for _, tt := range tests {
		got := floorDiv(tt.a, tt.b)
		if got != tt.want {
			t.Errorf("floorDiv(%d, %d) = %d, want %d", tt.a, tt.b, got, tt.want)
		}
	}
}
