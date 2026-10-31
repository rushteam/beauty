package roaring

import (
	"bytes"
	"reflect"
	"testing"
)

// TestMarshalRoaringKnownVector 用手工核算的字节向量断言与官方 RoaringFormatSpec 完全一致,
// 即可被 CRoaring/Java/pyroaring 等直接读取。位图 {1,2,3,100}(单 array 容器,无 run)。
func TestMarshalRoaringKnownVector(t *testing.T) {
	b := Of(1, 2, 3, 100)
	got, err := b.MarshalRoaring()
	if err != nil {
		t.Fatal(err)
	}
	want := []byte{
		0x3a, 0x30, 0x00, 0x00, // cookie = SERIAL_COOKIE_NO_RUNCONTAINER(12346)
		0x01, 0x00, 0x00, 0x00, // size = 1
		0x00, 0x00, 0x03, 0x00, // key=0, 基数-1=3
		0x10, 0x00, 0x00, 0x00, // offset = 16
		0x01, 0x00, 0x02, 0x00, 0x03, 0x00, 0x64, 0x00, // 1,2,3,100
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("官方格式字节不匹配\n got=%x\nwant=%x", got, want)
	}
}

func TestRoaringOfficialRoundTrip(t *testing.T) {
	cases := map[string]*Bitmap{
		"empty":  New(),
		"sparse": Of(1, 5, 9, 1000, 70000, 131072),
	}
	// dense:一个会升级为 bitmap 容器的高基数桶。
	dense := New()
	for i := uint32(0); i < 10000; i += 2 {
		dense.Add(i)
	}
	cases["dense"] = dense
	// run:连续区间,RunOptimize 后变 run 容器。
	run := New()
	for i := uint32(0); i < 50000; i++ {
		run.Add(i)
	}
	run.RunOptimize()
	cases["run"] = run
	// mixed:跨多个高位桶 + run。
	mixed := New()
	for i := uint32(0); i < 5; i++ {
		mixed.Add(i)
	}
	for i := uint32(65536); i < 70000; i++ {
		mixed.Add(i)
	}
	for i := uint32(200000); i < 260000; i++ {
		mixed.Add(i)
	}
	mixed.RunOptimize()
	cases["mixed"] = mixed

	for name, b := range cases {
		t.Run(name, func(t *testing.T) {
			data, err := b.MarshalRoaring()
			if err != nil {
				t.Fatal(err)
			}
			got := New()
			if err := got.UnmarshalRoaring(data); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got.ToArray(), b.ToArray()) {
				t.Fatalf("%s 往返不一致: len got=%d want=%d", name, got.Len(), b.Len())
			}
		})
	}
}

func TestRoaringOfficialBadCookie(t *testing.T) {
	b := New()
	if err := b.UnmarshalRoaring([]byte{0x01, 0x02, 0x03, 0x04}); err == nil {
		t.Fatal("期望 cookie 非法报错")
	}
	if err := b.UnmarshalRoaring([]byte{0x00}); err == nil {
		t.Fatal("期望截断报错")
	}
}
