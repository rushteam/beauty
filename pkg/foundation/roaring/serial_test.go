package roaring

import (
	"math/rand"
	"reflect"
	"testing"
)

func TestRoundTripSerialization(t *testing.T) {
	cases := []*Bitmap{
		New(),
		Of(1, 2, 3),
		Of(1, 70000, 140000, 3, 2),
	}
	// 稠密桶(位图容器)。
	dense := New()
	for i := uint32(0); i < 6000; i++ {
		dense.Add(i)
	}
	cases = append(cases, dense)

	// 连续大段 + RunOptimize(游程容器)。
	run := New()
	for i := uint32(1000); i < 50000; i++ {
		run.Add(i)
	}
	run.RunOptimize()
	if !run.containers[0].isRun() {
		t.Fatal("expected run container after RunOptimize")
	}
	cases = append(cases, run)

	for i, b := range cases {
		data, err := b.MarshalBinary()
		if err != nil {
			t.Fatalf("case %d marshal: %v", i, err)
		}
		var got Bitmap
		if err := got.UnmarshalBinary(data); err != nil {
			t.Fatalf("case %d unmarshal: %v", i, err)
		}
		if got.Len() != b.Len() {
			t.Fatalf("case %d len %d != %d", i, got.Len(), b.Len())
		}
		if !reflect.DeepEqual(got.ToArray(), b.ToArray()) {
			t.Fatalf("case %d values differ", i)
		}
	}
}

func TestRunOptimizeShrinksAndPreserves(t *testing.T) {
	b := New()
	for i := uint32(0); i < 40000; i++ {
		b.Add(i)
	}
	before, _ := b.MarshalBinary()
	b.RunOptimize()
	after, _ := b.MarshalBinary()
	if len(after) >= len(before) {
		t.Fatalf("run optimize should shrink contiguous data: before=%d after=%d", len(before), len(after))
	}
	// 内容不变。
	if b.Len() != 40000 || !b.Contains(0) || !b.Contains(39999) || b.Contains(40000) {
		t.Fatal("run optimize changed contents")
	}
}

func TestSerializationFuzz(t *testing.T) {
	rng := rand.New(rand.NewSource(99))
	for iter := 0; iter < 50; iter++ {
		b := New()
		for i := 0; i < 2000; i++ {
			b.Add(rng.Uint32() % 200000)
		}
		if rng.Intn(2) == 0 {
			b.RunOptimize()
		}
		data, _ := b.MarshalBinary()
		var got Bitmap
		if err := got.UnmarshalBinary(data); err != nil {
			t.Fatalf("iter %d: %v", iter, err)
		}
		if !reflect.DeepEqual(got.ToArray(), b.ToArray()) {
			t.Fatalf("iter %d mismatch", iter)
		}
	}
}

func TestUnmarshalBadData(t *testing.T) {
	var b Bitmap
	if err := b.UnmarshalBinary([]byte("xx")); err == nil {
		t.Fatal("expected error on bad magic")
	}
}
