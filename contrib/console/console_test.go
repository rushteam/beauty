package console

import (
	"testing"
)

func TestConsole_Register(t *testing.T) {
	c := New()
	c.Register(Command{
		Name: "test",
		Desc: "a test command",
		Handler: func(ctx Context) string {
			return "test output"
		},
	})
	c.mu.RLock()
	cmd, ok := c.commands["test"]
	c.mu.RUnlock()
	if !ok {
		t.Fatal("command not registered")
	}
	if cmd.Desc != "a test command" {
		t.Fatalf("desc=%q", cmd.Desc)
	}
}

func TestConsole_BuiltinHelp(t *testing.T) {
	c := New()
	cmd, ok := c.commands["help"]
	if !ok {
		t.Fatal("help command not registered")
	}
	result := cmd.Handler(Context{})
	if result == "" {
		t.Fatal("help should return non-empty output")
	}
	if len(result) < 10 {
		t.Fatalf("help output too short: %q", result)
	}
}

func TestConsole_BuiltinInfo(t *testing.T) {
	c := New()
	cmd := c.commands["info"]
	result := cmd.Handler(Context{})
	if result == "" {
		t.Fatal("info should return non-empty output")
	}
}

func TestConsole_BuiltinUptime(t *testing.T) {
	c := New()
	cmd := c.commands["uptime"]
	result := cmd.Handler(Context{})
	if result != "not started" {
		t.Fatalf("uptime before start should be 'not started', got %q", result)
	}
}

func TestConsole_RegisterTopic(t *testing.T) {
	c := New()
	c.RegisterTopic(Topic{
		Name:     "test-topic",
		Interval: 0,
		Fn:       func() string { return "data" },
	})
	c.mu.RLock()
	_, ok := c.topics["test-topic"]
	c.mu.RUnlock()
	if !ok {
		t.Fatal("topic not registered")
	}
}

func TestConsole_String(t *testing.T) {
	c := New()
	if c.String() != "console" {
		t.Fatalf("String()=%q", c.String())
	}
}

func TestConsole_DefaultConfig(t *testing.T) {
	c := New()
	if c.cfg.addr != ":9090" {
		t.Fatalf("default addr=%q", c.cfg.addr)
	}
	if c.cfg.path != "/ws" {
		t.Fatalf("default path=%q", c.cfg.path)
	}
}

func TestConsole_WithOptions(t *testing.T) {
	c := New(
		WithAddr(":8888"),
		WithPassword("secret"),
		WithPath("/console/ws"),
		WithUIPath("/console/"),
	)
	if c.cfg.addr != ":8888" {
		t.Fatalf("addr=%q", c.cfg.addr)
	}
	if c.cfg.password != "secret" {
		t.Fatalf("password=%q", c.cfg.password)
	}
	if c.cfg.path != "/console/ws" {
		t.Fatalf("path=%q", c.cfg.path)
	}
	if c.cfg.uiPath != "/console/" {
		t.Fatalf("uiPath=%q", c.cfg.uiPath)
	}
}

func TestConsole_CommandExecution(t *testing.T) {
	c := New()
	var executed bool
	c.Register(Command{
		Name: "exec",
		Desc: "test exec",
		Flag: FlagPublic,
		Handler: func(ctx Context) string {
			executed = true
			if len(ctx.Args) > 0 {
				return "got: " + ctx.Args[0]
			}
			return "no args"
		},
	})

	cmd := c.commands["exec"]
	result := cmd.Handler(Context{Args: []string{"hello"}})
	if !executed {
		t.Fatal("handler not executed")
	}
	if result != "got: hello" {
		t.Fatalf("result=%q", result)
	}
}
