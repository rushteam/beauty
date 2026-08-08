package conf

import (
	"context"
)

// expandingLoader wraps a Loader and expands secret placeholders after Unmarshal.
type expandingLoader struct {
	inner Loader
	opts  ExpandOptions
}

// WithSecrets returns a Loader that expands ${scheme:ref} placeholders in all
// string fields after each Unmarshal. Default providers are EnvProvider and
// FileProvider when providers is empty.
//
// Example config:
//
//	database:
//	  password: "${env:DB_PASSWORD}"
//	tls:
//	  key: "${file:/var/run/secrets/tls.key}"
//	token: "${secret:api_token}"   # requires a Provider with Scheme "secret"
func WithSecrets(inner Loader, providers ...Provider) Loader {
	opts := defaultExpandOptions(providers)
	if len(opts.Providers) == 0 {
		opts.Providers = []Provider{EnvProvider(), FileProvider()}
	}
	return &expandingLoader{inner: inner, opts: opts}
}

// WithSecretsOptions is WithSecrets with explicit ExpandOptions.
func WithSecretsOptions(inner Loader, opts ExpandOptions) Loader {
	if len(opts.Providers) == 0 {
		opts.Providers = []Provider{EnvProvider(), FileProvider()}
	}
	return &expandingLoader{inner: inner, opts: opts}
}

func (l *expandingLoader) Unmarshal(dst any) error {
	if err := l.inner.Unmarshal(dst); err != nil {
		return err
	}
	return ExpandOptionsApply(context.Background(), dst, l.opts)
}

func (l *expandingLoader) Watch(ctx context.Context, fn func()) {
	l.inner.Watch(ctx, fn)
}
