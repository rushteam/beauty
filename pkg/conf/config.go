package conf

import (
	"context"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"

	"github.com/fsnotify/fsnotify"
	"github.com/spf13/viper"
)

type Loader interface {
	Unmarshal(dst any) error
	Watch(ctx context.Context, fn func())
	// LoadAndWatch(ctx context.Context, key string, dst interface{}) error
}
type loader struct {
	driver string
	*viper.Viper
}

func (l *loader) Unmarshal(dst any) error {
	return l.Viper.Unmarshal(dst)
}

func (l *loader) Watch(ctx context.Context, fn func()) {
	go func() {
		if l.driver == "file" {
			l.Viper.OnConfigChange(func(in fsnotify.Event) {
				fn()
			})
			l.Viper.WatchConfig()
		}
		<-ctx.Done()
	}()
}

func New(rawURL string) (Loader, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("error parsing: %w", err)
	}
	if len(u.Scheme) == 0 {
		u.Scheme = "file"
	}
	l := &loader{
		driver: u.Scheme,
		Viper:  viper.New(),
	}
	l.SetConfigFile(u.Path)
	l.SetConfigType(strings.TrimPrefix(filepath.Ext(u.Path), "."))
	// l.AddConfigPath(filepath.Dir(filename))
	// l.SetConfigName(strings.TrimSuffix(filepath.Base(filename), filepath.Ext(filename)))
	if err := l.ReadInConfig(); err != nil {
		return l, err
	}
	return l, nil
}
