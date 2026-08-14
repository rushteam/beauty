package replicate_test

import (
	"testing"

	"github.com/rushteam/beauty/pkg/replicate"
)

func TestJournalCatchUp(t *testing.T) {
	j := replicate.NewJournal(8)
	for i := uint64(1); i <= 5; i++ {
		j.Record(replicate.Delta{Frame: i})
	}
	batch := j.CatchUp(2, 5)
	if batch.From != 2 || batch.To != 5 {
		t.Fatalf("range = (%d,%d]", batch.From, batch.To)
	}
	if len(batch.Deltas) != 3 {
		t.Fatalf("deltas = %d want 3", len(batch.Deltas))
	}
}

func TestViewerTrackOnAck(t *testing.T) {
	vt := replicate.NewViewerTrack(replicate.NewJournal(16))
	vt.RecordSent(replicate.Delta{Frame: 1})
	vt.RecordSent(replicate.Delta{Frame: 2})
	vt.RecordSent(replicate.Delta{Frame: 3})

	batch := vt.OnAck(replicate.Ack{LastFrame: 1})
	if len(batch.Deltas) != 2 {
		t.Fatalf("catchup deltas = %d want 2", len(batch.Deltas))
	}
	if batch.Deltas[0].Frame != 2 || batch.Deltas[1].Frame != 3 {
		t.Fatalf("frames = %v", []uint64{batch.Deltas[0].Frame, batch.Deltas[1].Frame})
	}
	if vt.Gap() != 2 {
		t.Fatalf("gap after ack=1 = %d want 2", vt.Gap())
	}
	batch = vt.OnAck(replicate.Ack{LastFrame: 3})
	if len(batch.Deltas) != 0 {
		t.Fatalf("catchup after full ack = %d want 0", len(batch.Deltas))
	}
	if vt.Gap() != 0 {
		t.Fatalf("gap after ack=3 = %d", vt.Gap())
	}
}
