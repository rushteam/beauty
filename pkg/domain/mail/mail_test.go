package mail

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

type testAttachment struct {
	ItemID string
	Count  int
}

func newMailbox() *Mailbox[testAttachment] {
	store := NewMemoryStore[testAttachment]()
	return NewMailbox[testAttachment](store, WithMaxPerUser(10), WithDefaultTTL(24*time.Hour))
}

func makeMail(id, recipient string, attachments ...testAttachment) *Mail[testAttachment] {
	return &Mail[testAttachment]{
		ID:          id,
		RecipientID: recipient,
		SenderID:    "system",
		Title:       "Test Mail " + id,
		Body:        "Hello",
		Attachments: attachments,
	}
}

func TestSend_And_List(t *testing.T) {
	mb := newMailbox()
	m := makeMail("m1", "player1", testAttachment{"gold", 100})
	if err := mb.Send(m); err != nil {
		t.Fatal(err)
	}

	mails, err := mb.List("player1", Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(mails) != 1 {
		t.Fatalf("list len = %d, want 1", len(mails))
	}
	if mails[0].Status != StatusUnread {
		t.Fatalf("status = %v, want unread", mails[0].Status)
	}
}

func TestRead(t *testing.T) {
	mb := newMailbox()
	mb.Send(makeMail("m1", "player1"))

	if err := mb.Read("m1"); err != nil {
		t.Fatal(err)
	}

	mails, _ := mb.List("player1", Filter{StatusIn: []Status{StatusRead}})
	if len(mails) != 1 {
		t.Fatalf("read mails = %d, want 1", len(mails))
	}
}

func TestClaim_Success(t *testing.T) {
	var claimed bool
	store := NewMemoryStore[testAttachment]()
	mb := NewMailbox[testAttachment](store,
		WithOnClaim(func(mailID, recipientID string) {
			claimed = true
		}),
	)

	mb.Send(makeMail("m1", "player1", testAttachment{"gem", 50}))

	att, err := mb.Claim("m1")
	if err != nil {
		t.Fatal(err)
	}
	if len(att) != 1 || att[0].Count != 50 {
		t.Fatalf("attachments = %v", att)
	}
	if !claimed {
		t.Fatal("onClaim not called")
	}
}

func TestClaim_AlreadyClaimed(t *testing.T) {
	mb := newMailbox()
	mb.Send(makeMail("m1", "player1", testAttachment{"gem", 1}))
	mb.Claim("m1")

	_, err := mb.Claim("m1")
	if err != ErrAlreadyClaimed {
		t.Fatalf("got %v, want ErrAlreadyClaimed", err)
	}
}

func TestClaim_NoAttachment(t *testing.T) {
	mb := newMailbox()
	mb.Send(makeMail("m1", "player1"))

	_, err := mb.Claim("m1")
	if err != ErrNoAttachment {
		t.Fatalf("got %v, want ErrNoAttachment", err)
	}
}

func TestClaim_Expired(t *testing.T) {
	store := NewMemoryStore[testAttachment]()
	mb := NewMailbox[testAttachment](store, WithDefaultTTL(time.Millisecond))

	mb.Send(makeMail("m1", "player1", testAttachment{"gem", 1}))
	time.Sleep(5 * time.Millisecond)

	_, err := mb.Claim("m1")
	if err != ErrExpired {
		t.Fatalf("got %v, want ErrExpired", err)
	}
}

func TestUnread(t *testing.T) {
	mb := newMailbox()
	mb.Send(makeMail("m1", "player1"))
	mb.Send(makeMail("m2", "player1"))
	mb.Send(makeMail("m3", "player1"))
	mb.Read("m2")

	n, err := mb.Unread("player1")
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("unread = %d, want 2", n)
	}
}

func TestDelete(t *testing.T) {
	mb := newMailbox()
	mb.Send(makeMail("m1", "player1"))

	if err := mb.Delete("m1"); err != nil {
		t.Fatal(err)
	}
	_, err := mb.Claim("m1")
	if err != ErrNotFound {
		t.Fatalf("got %v after delete, want ErrNotFound", err)
	}
}

func TestDeleteExpired(t *testing.T) {
	store := NewMemoryStore[testAttachment]()
	mb := NewMailbox[testAttachment](store, WithDefaultTTL(time.Millisecond))

	mb.Send(makeMail("m1", "player1"))
	mb.Send(makeMail("m2", "player1"))
	time.Sleep(5 * time.Millisecond)

	n, err := mb.DeleteExpired()
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("deleted = %d, want 2", n)
	}
}

func TestMailboxFull(t *testing.T) {
	mb := newMailbox() // maxPerUser=10
	for i := 0; i < 10; i++ {
		mb.Send(makeMail(fmt.Sprintf("m%d", i), "player1"))
	}
	err := mb.Send(makeMail("overflow", "player1"))
	if err != ErrMailboxFull {
		t.Fatalf("got %v, want ErrMailboxFull", err)
	}
}

func TestBatchSend(t *testing.T) {
	store := NewMemoryStore[testAttachment]()
	mb := NewMailbox[testAttachment](store, WithMaxPerUser(100))

	seq := 0
	idGen := func() string {
		seq++
		return fmt.Sprintf("batch-%d", seq)
	}

	template := &Mail[testAttachment]{
		SenderID:    "admin",
		Title:       "Maintenance Reward",
		Body:        "Here's your reward",
		Attachments: []testAttachment{{"gold", 500}},
	}

	errs := mb.BatchSend([]string{"p1", "p2", "p3"}, template, idGen)
	if len(errs) != 0 {
		t.Fatalf("batch errors: %v", errs)
	}

	for _, pid := range []string{"p1", "p2", "p3"} {
		mails, _ := mb.List(pid, Filter{})
		if len(mails) != 1 {
			t.Fatalf("player %s mails = %d, want 1", pid, len(mails))
		}
	}
}

func TestListFilter_Pagination(t *testing.T) {
	mb := newMailbox()
	for i := 0; i < 5; i++ {
		mb.Send(makeMail(fmt.Sprintf("m%d", i), "player1"))
	}

	mails, _ := mb.List("player1", Filter{Limit: 2, Offset: 1})
	if len(mails) != 2 {
		t.Fatalf("paginated list = %d, want 2", len(mails))
	}
}

func TestListFilter_ByStatus(t *testing.T) {
	mb := newMailbox()
	mb.Send(makeMail("m1", "player1"))
	mb.Send(makeMail("m2", "player1"))
	mb.Read("m1")

	mails, _ := mb.List("player1", Filter{StatusIn: []Status{StatusUnread}})
	if len(mails) != 1 || mails[0].ID != "m2" {
		t.Fatalf("filtered list: %v", mails)
	}
}

func TestList_ExpiresFiltered(t *testing.T) {
	store := NewMemoryStore[testAttachment]()
	mb := NewMailbox[testAttachment](store, WithDefaultTTL(time.Millisecond))
	mb.Send(makeMail("m1", "player1"))
	time.Sleep(5 * time.Millisecond)

	mails, _ := mb.List("player1", Filter{})
	if len(mails) != 0 {
		t.Fatalf("expired mails should be filtered, got %d", len(mails))
	}
}

func TestConcurrent(t *testing.T) {
	store := NewMemoryStore[testAttachment]()
	mb := NewMailbox[testAttachment](store, WithMaxPerUser(1000))

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			mb.Send(makeMail(fmt.Sprintf("c%d", n), "player1", testAttachment{"x", n}))
		}(i)
	}
	wg.Wait()

	for i := 0; i < 25; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			mb.Claim(fmt.Sprintf("c%d", n))
		}(i)
	}
	wg.Wait()

	// No panics, verify some state
	mails, _ := mb.List("player1", Filter{})
	if len(mails) == 0 {
		t.Fatal("expected some mails")
	}
}
