package conf

import (
	"context"
	"log/slog"
	"path/filepath"
	"strings"

	"github.com/fsnotify/fsnotify"
	"github.com/spf13/viper"
)

func init() {
	RegistryLoader("file", &FileLoader{})
}

type FileLoader struct {
	*viper.Viper
}

func (l *FileLoader) Load(key string, dst interface{}) error {
	if len(key) == 0 {
		if err := l.Viper.Unmarshal(dst); err != nil {
			return err
		}
	}
	if err := l.Viper.UnmarshalKey(key, dst); err != nil {
		return err
	}
	return nil
}

func (l *FileLoader) LoadAndWatch(ctx context.Context, key string, dst interface{}) error {
	if err := l.Load(key, dst); err != nil {
		return err
	}
	go func() {
		l.Viper.WatchConfig()
		l.Viper.OnConfigChange(func(in fsnotify.Event) {
			if err := l.Load(key, dst); err != nil {
				slog.Error("watch config load failed: %v", err)
			}
		})
		<-ctx.Done()
	}()
	return nil
}

func NewFileLoader(filename string) (Loader, error) {
	v := viper.New()
	ext := strings.TrimPrefix(filepath.Ext(filename), ".")
	v.SetConfigType(ext)
	v.AddConfigPath(filepath.Dir(filename))
	v.SetConfigName(strings.TrimSuffix(filepath.Base(filename), filepath.Ext(filename)))
	if err := v.ReadInConfig(); err != nil {
		return nil, err
	}
	return &FileLoader{
		Viper: v,
	}, nil
}
