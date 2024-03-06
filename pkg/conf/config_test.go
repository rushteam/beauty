package conf_test

import (
	"context"
	"testing"
	"time"

	"github.com/rushteam/beauty/pkg/conf"
)

func TestNew(t *testing.T) {
	l, err := conf.New("file")
	if err != nil {
		t.Error(err)
		return
	}
	c := struct{}{}
	if err := l.Load("t.yaml", &c); err != nil {
		t.Error(err)
		return
	}
}

func TestNewFile_Load(t *testing.T) {
	l, err := conf.NewFileLoader("t.yaml")
	if err != nil {
		t.Error(err)
		return
	}
	c := struct {
		App string `mapstructure:"app"`
	}{}
	if err := l.Load("", &c); err != nil {
		t.Error(err)
		return
	}
}

func TestNewFile_LoadWatch(t *testing.T) {
	l, err := conf.NewFileLoader("t.yaml")
	if err != nil {
		t.Error(err)
		return
	}
	c := struct {
		App string `mapstructure:"app"`
	}{}
	ctx, cancel := context.WithCancel(context.Background())
	if err := l.LoadAndWatch(ctx, "", &c); err != nil {
		t.Error(err)
		return
	}
	time.Sleep(2 * time.Second)
	cancel()
	t.Fail()
}

func TestNewNacos_LoadWatch(t *testing.T) {
	l, err := conf.NewNacosLoader()
	if err != nil {
		t.Error(err)
		return
	}
	c := struct {
		App string `mapstructure:"app"`
	}{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := l.LoadAndWatch(ctx, "", &c); err != nil {
		t.Error(err)
		return
	}
	time.Sleep(2 * time.Second)
	t.Fail()
}
