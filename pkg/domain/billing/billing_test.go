package billing_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/rushteam/beauty/pkg/domain/billing"
)

func TestRegistryVerifyAndNotice(t *testing.T) {
	r := billing.New()
	err := r.Add("WeChat", billing.Channel{
		Hook: "wx",
		Verify: func(_ context.Context, claim billing.Claim) (billing.Order, error) {
			return billing.Order{Ref: "o1", UserID: claim.UserID, Product: claim.Product}, nil
		},
		VerifyPlan: func(_ context.Context, claim billing.Claim) (billing.Plan, error) {
			return billing.Plan{Ref: "s1", UserID: claim.UserID, Product: claim.Product, Active: true, Until: time.Unix(100, 0)}, nil
		},
		ParseNotice: func(_ context.Context, notice billing.Notice) ([]billing.Change, error) {
			if string(notice.Body) != "refund" {
				return nil, errors.New("billing test: bad body")
			}
			return []billing.Change{{Kind: billing.KindRefunded, Ref: "o1", UserID: "u1"}}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Add("wechat", billing.Channel{Verify: func(context.Context, billing.Claim) (billing.Order, error) {
		return billing.Order{}, nil
	}}); !errors.Is(err, billing.ErrExists) {
		t.Fatalf("duplicate = %v", err)
	}

	order, err := r.Verify(context.Background(), "wechat", billing.Claim{UserID: "u1", Product: "gem"})
	if err != nil {
		t.Fatal(err)
	}
	if order.Ref != "o1" || order.Product != "gem" {
		t.Fatalf("order = %+v", order)
	}
	plan, err := r.VerifyPlan(context.Background(), "WECHAT", billing.Claim{UserID: "u1", Product: "vip"})
	if err != nil || !plan.Active || plan.Product != "vip" {
		t.Fatalf("plan = %+v err=%v", plan, err)
	}
	changes, err := r.ParseHook(context.Background(), "WX", billing.Notice{Body: []byte("refund")})
	if err != nil || len(changes) != 1 || changes[0].Kind != billing.KindRefunded {
		t.Fatalf("changes = %+v err=%v", changes, err)
	}
	if _, err := r.Verify(context.Background(), "missing", billing.Claim{}); !errors.Is(err, billing.ErrNotFound) {
		t.Fatalf("missing = %v", err)
	}
}

func TestRegistryOptionalFuncs(t *testing.T) {
	r := billing.New()
	if err := r.Add("only-buy", billing.Channel{
		Verify: func(context.Context, billing.Claim) (billing.Order, error) {
			return billing.Order{Ref: "x"}, nil
		},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.VerifyPlan(context.Background(), "only-buy", billing.Claim{}); !errors.Is(err, billing.ErrNoPlan) {
		t.Fatalf("plan = %v", err)
	}
	if _, err := r.ParseNotice(context.Background(), "only-buy", billing.Notice{}); !errors.Is(err, billing.ErrNoNotice) {
		t.Fatalf("notice = %v", err)
	}
	if err := r.Add("bad name", billing.Channel{Verify: func(context.Context, billing.Claim) (billing.Order, error) {
		return billing.Order{}, nil
	}}); !errors.Is(err, billing.ErrInvalidName) {
		t.Fatalf("name = %v", err)
	}
	if err := r.Add("empty", billing.Channel{}); !errors.Is(err, billing.ErrEmpty) {
		t.Fatalf("empty = %v", err)
	}
}

func TestRegistryConcurrentVerify(t *testing.T) {
	r := billing.New()
	if err := r.Add("store", billing.Channel{
		Verify: func(context.Context, billing.Claim) (billing.Order, error) {
			return billing.Order{Ref: "ok"}, nil
		},
	}); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := r.Verify(context.Background(), "store", billing.Claim{}); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
}
