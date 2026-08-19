package graphql_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/99designs/gqlgen/graphql"
	gqlparser "github.com/vektah/gqlparser/v2"
	"github.com/vektah/gqlparser/v2/ast"

	gql "github.com/rushteam/beauty/contrib/graphql"
)

type stubSchema struct {
	schema *ast.Schema
}

func (s stubSchema) Schema() *ast.Schema { return s.schema }

func (stubSchema) Complexity(_ context.Context, _, _ string, childComplexity int, _ map[string]any) (int, bool) {
	return childComplexity, true
}

func (stubSchema) Exec(_ context.Context) graphql.ResponseHandler {
	return func(_ context.Context) *graphql.Response {
		return &graphql.Response{Data: []byte(`{"hello":"world"}`)}
	}
}

func testSchema(t *testing.T) graphql.ExecutableSchema {
	t.Helper()
	schema, err := gqlparser.LoadSchema(&ast.Source{
		Name:  "stub.graphql",
		Input: `type Query { hello: String! }`,
	})
	if err != nil {
		t.Fatalf("load schema: %v", err)
	}
	return stubSchema{schema: schema}
}

func TestNew_DefaultOptions(t *testing.T) {
	srv := gql.New("127.0.0.1:0", testSchema(t))
	if srv == nil {
		t.Fatal("New returned nil")
	}
	if got := srv.String(); got != "graphql.Server(graphql)" {
		t.Fatalf("String() = %q, want graphql.Server(graphql)", got)
	}
	if srv.Name() != "graphql" {
		t.Fatalf("Name() = %q", srv.Name())
	}
	if srv.Kind() != "http" {
		t.Fatalf("Kind() = %q", srv.Kind())
	}
	meta := srv.Metadata()
	if meta["protocol"] != "graphql" {
		t.Fatalf("Metadata[protocol] = %q", meta["protocol"])
	}
}

func TestNew_WithOptions(t *testing.T) {
	mw := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("X-Test", "1")
			next.ServeHTTP(w, r)
		})
	}
	srv := gql.New("127.0.0.1:0", testSchema(t),
		gql.WithName("bff"),
		gql.WithID("svc-1"),
		gql.WithVersion("v2"),
		gql.WithPlayground(true),
		gql.WithGraphQLPath("/gql"),
		gql.WithMiddleware(mw),
		gql.WithShutdownTimeout(time.Second),
	)
	if srv.Name() != "bff" {
		t.Fatalf("Name() = %q", srv.Name())
	}
	if srv.ID() != "svc-1" {
		t.Fatalf("ID() = %q", srv.ID())
	}
	if srv.Metadata()["version"] != "v2" {
		t.Fatalf("Metadata[version] = %q", srv.Metadata()["version"])
	}
	if srv.Handler() == nil {
		t.Fatal("Handler() returned nil")
	}
}

func TestServer_ServiceShape(t *testing.T) {
	var svc interface {
		Start(context.Context) error
		String() string
	} = gql.New("127.0.0.1:0", testSchema(t))
	if svc.String() == "" {
		t.Fatal("String should not be empty")
	}
}

func TestServer_ReadyNotifier(t *testing.T) {
	srv := gql.New("127.0.0.1:0", testSchema(t))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() { _ = srv.Start(ctx) }()

	select {
	case <-srv.Ready():
	case <-time.After(2 * time.Second):
		t.Fatal("Ready channel not closed after Start")
	}
}

func TestServer_StartAndShutdown(t *testing.T) {
	srv := gql.New("127.0.0.1:0", testSchema(t))
	ctx, cancel := context.WithCancel(context.Background())

	errCh := make(chan error, 1)
	go func() { errCh <- srv.Start(ctx) }()

	select {
	case <-srv.Ready():
	case <-time.After(2 * time.Second):
		t.Fatal("server did not become ready")
	}
	if srv.Addr() == "" {
		t.Fatal("Addr() should be set after listen")
	}

	cancel()
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("Start returned error on shutdown: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("server did not shut down")
	}
}
