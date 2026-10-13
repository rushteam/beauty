package conf

import (
	"context"
	"fmt"
	"os"
	"reflect"
	"regexp"
	"strings"
)

// placeholderRE matches any ${...} placeholder; the inner body is parsed by
// parsePlaceholder to support both forms:
//
//	${scheme:reference}            e.g. ${env:NAME} ${file:/path} ${secret:key}
//	${scheme:reference:-default}   e.g. ${env:NAME:-8080}
//	${VAR}                         bare var, resolved via the env provider
//	${VAR:-default}                bare var with shell-style default
var placeholderRE = regexp.MustCompile(`\$\{([^}]+)\}`)

// Provider resolves a secret reference for one scheme (env, file, vault, …).
type Provider interface {
	Scheme() string
	// Get returns the secret value for key (already stripped of scheme prefix).
	// key is the part after "scheme:" and before optional ":-default".
	Get(ctx context.Context, key string) (string, error)
}

// ExpandOptions controls placeholder expansion.
type ExpandOptions struct {
	Providers []Provider
	// Strict when true (default) returns error if a placeholder cannot be resolved
	// and has no default. When false, unresolved placeholders are left as-is.
	Strict bool
}

func defaultExpandOptions(providers []Provider) ExpandOptions {
	return ExpandOptions{Providers: providers, Strict: true}
}

// EnvProvider resolves ${env:NAME} and ${env:NAME:-default} from process environment.
func EnvProvider() Provider { return envProvider{} }

type envProvider struct{}

func (envProvider) Scheme() string { return "env" }

func (envProvider) Get(_ context.Context, key string) (string, error) {
	v, ok := os.LookupEnv(key)
	if !ok {
		return "", fmt.Errorf("conf: env %q not set", key)
	}
	return v, nil
}

// FileProvider resolves ${file:/path/to/secret} by reading the file contents (trimmed).
func FileProvider() Provider { return fileProvider{} }

type fileProvider struct{}

func (fileProvider) Scheme() string { return "file" }

func (fileProvider) Get(_ context.Context, key string) (string, error) {
	b, err := os.ReadFile(key)
	if err != nil {
		return "", fmt.Errorf("conf: read file %q: %w", key, err)
	}
	return strings.TrimRight(string(b), "\r\n"), nil
}

// MapProvider resolves ${scheme:key} from an in-memory map (tests / simple vault stubs).
func MapProvider(scheme string, values map[string]string) Provider {
	return mapProvider{scheme: scheme, values: values}
}

type mapProvider struct {
	scheme string
	values map[string]string
}

func (p mapProvider) Scheme() string { return p.scheme }

func (p mapProvider) Get(_ context.Context, key string) (string, error) {
	v, ok := p.values[key]
	if !ok {
		return "", fmt.Errorf("conf: %s %q not found", p.scheme, key)
	}
	return v, nil
}

// ExpandString replaces all ${scheme:ref} / ${scheme:ref:-default} placeholders in s.
// When providers is empty, EnvProvider and FileProvider are used.
func ExpandString(ctx context.Context, s string, providers ...Provider) (string, error) {
	opts := defaultExpandOptions(providers)
	if len(opts.Providers) == 0 {
		opts.Providers = []Provider{EnvProvider(), FileProvider()}
	}
	return expandString(ctx, s, opts)
}

// ExpandStringOptions is ExpandString with explicit options.
func ExpandStringOptions(ctx context.Context, s string, opts ExpandOptions) (string, error) {
	if len(opts.Providers) == 0 {
		opts.Providers = []Provider{EnvProvider(), FileProvider()}
	}
	return expandString(ctx, s, opts)
}

func expandString(ctx context.Context, s string, opts ExpandOptions) (string, error) {
	index := indexProviders(opts.Providers)
	var firstErr error
	out := placeholderRE.ReplaceAllStringFunc(s, func(match string) string {
		if firstErr != nil {
			return match
		}
		sub := placeholderRE.FindStringSubmatch(match)
		if len(sub) != 2 {
			return match
		}
		scheme, key, def, hasDef := parsePlaceholder(sub[1])
		p, ok := index[scheme]
		if !ok {
			if hasDef {
				return def
			}
			if opts.Strict {
				firstErr = fmt.Errorf("conf: no provider for scheme %q (placeholder %s)", scheme, match)
			}
			return match
		}
		val, err := p.Get(ctx, key)
		if err != nil {
			if hasDef {
				return def
			}
			if opts.Strict {
				firstErr = fmt.Errorf("conf: resolve %s: %w", match, err)
			}
			return match
		}
		return val
	})
	return out, firstErr
}

// parsePlaceholder 解析 ${...} 内部内容，返回 provider scheme、key、默认值。
//
//   - 先用 ":-" 切出 shell 风格默认值（同 bash 的 ${VAR:-default}）。
//   - 再用第一个 ":" 切出 scheme；没有 ":" 时视为裸变量，默认走 env provider。
//
// 示例：
//
//	env:NAME         -> scheme=env  key=NAME
//	env:NAME:-8080   -> scheme=env  key=NAME  def=8080
//	file:/a/b        -> scheme=file key=/a/b
//	PORT             -> scheme=env  key=PORT   （裸变量）
//	PORT:-8080       -> scheme=env  key=PORT   def=8080
func parsePlaceholder(inner string) (scheme, key, def string, hasDef bool) {
	body := inner
	if i := strings.Index(inner, ":-"); i >= 0 {
		body, def, hasDef = inner[:i], inner[i+2:], true
	}
	if i := strings.Index(body, ":"); i >= 0 {
		return body[:i], body[i+1:], def, hasDef
	}
	// 裸变量 ${VAR} / ${VAR:-default}：默认按环境变量解析。
	return "env", body, def, hasDef
}

func indexProviders(providers []Provider) map[string]Provider {
	m := make(map[string]Provider, len(providers))
	for _, p := range providers {
		if p == nil {
			continue
		}
		m[p.Scheme()] = p
	}
	return m
}

// Expand walks v (struct / map / slice / pointer) and expands placeholders in all string fields.
// Non-string values are left unchanged. Unexported struct fields are skipped.
func Expand(ctx context.Context, v any, providers ...Provider) error {
	return ExpandOptionsApply(ctx, v, defaultExpandOptions(providers))
}

// ExpandOptionsApply is Expand with explicit options.
func ExpandOptionsApply(ctx context.Context, v any, opts ExpandOptions) error {
	if len(opts.Providers) == 0 {
		opts.Providers = []Provider{EnvProvider(), FileProvider()}
	}
	rv := reflect.ValueOf(v)
	if rv.Kind() != reflect.Pointer || rv.IsNil() {
		return fmt.Errorf("conf: Expand requires a non-nil pointer")
	}
	return expandValue(ctx, rv.Elem(), opts)
}

func expandValue(ctx context.Context, rv reflect.Value, opts ExpandOptions) error {
	if !rv.IsValid() {
		return nil
	}
	switch rv.Kind() {
	case reflect.String:
		if !rv.CanSet() {
			return nil
		}
		expanded, err := expandString(ctx, rv.String(), opts)
		if err != nil {
			return err
		}
		rv.SetString(expanded)
		return nil
	case reflect.Pointer:
		if rv.IsNil() {
			return nil
		}
		return expandValue(ctx, rv.Elem(), opts)
	case reflect.Struct:
		rt := rv.Type()
		for i := 0; i < rv.NumField(); i++ {
			if !rt.Field(i).IsExported() {
				continue
			}
			if err := expandValue(ctx, rv.Field(i), opts); err != nil {
				return err
			}
		}
		return nil
	case reflect.Slice, reflect.Array:
		for i := 0; i < rv.Len(); i++ {
			if err := expandValue(ctx, rv.Index(i), opts); err != nil {
				return err
			}
		}
		return nil
	case reflect.Map:
		if rv.Type().Key().Kind() != reflect.String {
			return nil
		}
		for _, key := range rv.MapKeys() {
			val := rv.MapIndex(key)
			// Map values may not be addressable; copy-expand-set for strings / structs.
			if err := expandMapValue(ctx, rv, key, val, opts); err != nil {
				return err
			}
		}
		return nil
	case reflect.Interface:
		if rv.IsNil() {
			return nil
		}
		return expandValue(ctx, rv.Elem(), opts)
	default:
		return nil
	}
}

func expandMapValue(ctx context.Context, m, key, val reflect.Value, opts ExpandOptions) error {
	switch val.Kind() {
	case reflect.String:
		expanded, err := expandString(ctx, val.String(), opts)
		if err != nil {
			return err
		}
		m.SetMapIndex(key, reflect.ValueOf(expanded))
		return nil
	case reflect.Pointer, reflect.Struct, reflect.Slice, reflect.Map, reflect.Interface:
		// Need addressable copy for nested mutation.
		cp := reflect.New(val.Type()).Elem()
		cp.Set(val)
		if err := expandValue(ctx, cp, opts); err != nil {
			return err
		}
		m.SetMapIndex(key, cp)
		return nil
	default:
		return nil
	}
}
