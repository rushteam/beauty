package identity_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/rushteam/beauty/pkg/domain/identity"
)

func resolver(id string) identity.Resolver {
	return identity.ResolverFunc(func(_ context.Context, cred identity.Credential) (identity.Principal, error) {
		if cred.Token == "" {
			return identity.Principal{}, errors.New("identity test: empty token")
		}
		return identity.Principal{ExternalID: id, Name: "Ada", Vars: map[string]string{"from": cred.Token}}, nil
	})
}

func TestSignInBindRelease(t *testing.T) {
	d := identity.New()
	if err := d.Add("Apple", resolver("ext-1")); err != nil {
		t.Fatal(err)
	}
	if err := d.Add("apple", resolver("other")); !errors.Is(err, identity.ErrExists) {
		t.Fatalf("dup = %v", err)
	}

	_, err := d.SignIn(context.Background(), "apple", identity.Credential{Token: "tok"})
	var unbound *identity.UnboundError
	if !errors.As(err, &unbound) || unbound.Principal.ExternalID != "ext-1" {
		t.Fatalf("sign-in = %v", err)
	}
	if err := d.Bind("acc-1", "APPLE", unbound.Principal); err != nil {
		t.Fatal(err)
	}
	got, err := d.SignIn(context.Background(), "Apple", identity.Credential{Token: "tok"})
	if err != nil {
		t.Fatal(err)
	}
	if got.AccountID != "acc-1" || got.Channel != "Apple" || got.Vars["from"] != "tok" {
		t.Fatalf("binding = %+v", got)
	}
	got.Vars["from"] = "mutated"
	again, _ := d.Find("apple", "ext-1")
	if again.Vars["from"] != "tok" {
		t.Fatalf("vars leaked: %+v", again.Vars)
	}

	if err := d.Bind("acc-2", "apple", identity.Principal{ExternalID: "ext-1"}); !errors.Is(err, identity.ErrTaken) {
		t.Fatalf("taken = %v", err)
	}
	if err := d.Bind("acc-1", "apple", identity.Principal{ExternalID: "ext-2"}); !errors.Is(err, identity.ErrBound) {
		t.Fatalf("bound = %v", err)
	}
	if err := d.Bind("acc-1", "apple", identity.Principal{ExternalID: "ext-1", Name: "Ada Lovelace"}); err != nil {
		t.Fatal(err)
	}
	if err := d.Release("acc-1", "apple"); err != nil {
		t.Fatal(err)
	}
	if _, ok := d.Find("apple", "ext-1"); ok {
		t.Fatal("still bound")
	}
	if err := d.Release("acc-1", "apple"); !errors.Is(err, identity.ErrNotFound) {
		t.Fatalf("second release = %v", err)
	}
}

func TestListAndUnknownChannel(t *testing.T) {
	d := identity.New()
	_ = d.Add("device", resolver("dev-9"))
	_ = d.Add("wechat", resolver("wx-9"))
	if err := d.Bind("acc", "device", identity.Principal{ExternalID: "dev-9"}); err != nil {
		t.Fatal(err)
	}
	if err := d.Bind("acc", "wechat", identity.Principal{ExternalID: "wx-9"}); err != nil {
		t.Fatal(err)
	}
	list := d.List("acc")
	if len(list) != 2 || list[0].Channel != "device" || list[1].Channel != "wechat" {
		t.Fatalf("list = %+v", list)
	}
	if _, err := d.Resolve(context.Background(), "nope", identity.Credential{Token: "x"}); !errors.Is(err, identity.ErrNotFound) {
		t.Fatalf("unknown = %v", err)
	}
	if err := d.Bind("acc", "nope", identity.Principal{ExternalID: "z"}); !errors.Is(err, identity.ErrNotFound) {
		t.Fatalf("bind unknown = %v", err)
	}
}

func TestBindConcurrent(t *testing.T) {
	d := identity.New()
	if err := d.Add("device", resolver("unused")); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			id := fmt.Sprintf("acc-%d", i)
			ext := id
			if err := d.Bind(id, "device", identity.Principal{ExternalID: ext}); err != nil {
				t.Error(err)
			}
			if _, ok := d.Find("device", ext); !ok {
				t.Errorf("missing %s", ext)
			}
		}(i)
	}
	wg.Wait()
}
