package dberr_test

import (
	"database/sql"
	stderrors "errors"
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/rushteam/beauty/pkg/dberr"
	perr "github.com/rushteam/beauty/pkg/errors"
)

// fakeDriver 模拟一个驱动的 Classify:按哨兵 error 归类。
type fakeDriver struct{}

var (
	errUnique    = stderrors.New("duplicate key value violates unique constraint")
	errFK        = stderrors.New("violates foreign key constraint")
	errNotFound  = stderrors.New("no rows in result set")
	errDeadlock  = stderrors.New("deadlock detected")
	errTimeout   = stderrors.New("context deadline exceeded")
	errConnReset = stderrors.New("connection reset by peer")
)

func (fakeDriver) Classify(err error) dberr.ErrClass {
	switch {
	case stderrors.Is(err, errUnique):
		return dberr.ClassUniqueViolation
	case stderrors.Is(err, errFK):
		return dberr.ClassForeignKeyViolation
	case stderrors.Is(err, errNotFound):
		return dberr.ClassNotFound
	case stderrors.Is(err, errDeadlock):
		return dberr.ClassDeadlock
	case stderrors.Is(err, errTimeout):
		return dberr.ClassTimeout
	case stderrors.Is(err, errConnReset):
		return dberr.ClassConnection
	}
	return dberr.ClassUnknown
}

func newT(t *testing.T) *dberr.Translator {
	t.Helper()
	return dberr.New(dberr.WithDriver(fakeDriver{}))
}

func TestDBErr_Translate_UniqueViolation(t *testing.T) {
	tr := newT(t)
	s := tr.Translate(errUnique)
	if s.Code() != perr.CodeConflict {
		t.Fatalf("unique→want Conflict, got %d", s.Code())
	}
	if !stderrors.Is(s.Cause(), errUnique) {
		t.Fatal("cause lost")
	}
}

func TestDBErr_Translate_NotFound(t *testing.T) {
	tr := newT(t)
	s := tr.Translate(errNotFound)
	if s.Code() != perr.CodeNotFound {
		t.Fatalf("notfound→want NotFound, got %d", s.Code())
	}
}

func TestDBErr_Translate_Timeout(t *testing.T) {
	tr := newT(t)
	s := tr.Translate(errTimeout)
	if s.Code() != perr.CodeDeadline {
		t.Fatalf("timeout→want Deadline, got %d", s.Code())
	}
}

func TestDBErr_Translate_Connection(t *testing.T) {
	tr := newT(t)
	s := tr.Translate(errConnReset)
	if s.Code() != perr.CodeUnavailable {
		t.Fatalf("conn→want Unavailable, got %d", s.Code())
	}
}

func TestDBErr_Translate_Wrapped(t *testing.T) {
	tr := newT(t)
	// 用 fmt.Errorf 包一层,errors.Is 仍能命中。
	wrapped := fmt.Errorf("query failed: %w", errUnique)
	s := tr.Translate(wrapped)
	if s.Code() != perr.CodeConflict {
		t.Fatalf("wrapped unique→want Conflict, got %d", s.Code())
	}
}

func TestDBErr_Translate_StatusPassthrough(t *testing.T) {
	tr := newT(t)
	orig := perr.New(perr.CodeForbidden, "nope")
	s := tr.Translate(orig)
	if s != orig {
		t.Fatal("already-*Status should pass through")
	}
}

func TestDBErr_Translate_Nil(t *testing.T) {
	tr := newT(t)
	if tr.Translate(nil) != nil {
		t.Fatal("nil in → nil out")
	}
}

func TestDBErr_Translate_Unknown(t *testing.T) {
	tr := newT(t)
	s := tr.Translate(stderrors.New("weird driver error"))
	if s.Code() != perr.CodeInternal {
		t.Fatalf("unknown→want Internal, got %d", s.Code())
	}
}

func TestDBErr_Translate_NoDriver(t *testing.T) {
	tr := dberr.New() // 无 driver
	s := tr.Translate(stderrors.New("anything"))
	if s.Code() != perr.CodeInternal {
		t.Fatalf("no driver→want Internal, got %d", s.Code())
	}
}

func TestDBErr_WithMapping_Override(t *testing.T) {
	// 把 UniqueViolation 改映射到 InvalidArgument。
	tr := dberr.New(
		dberr.WithDriver(fakeDriver{}),
		dberr.WithMapping(dberr.ClassUniqueViolation, perr.CodeInvalidArgument),
	)
	s := tr.Translate(errUnique)
	if s.Code() != perr.CodeInvalidArgument {
		t.Fatalf("overridden→want InvalidArgument, got %d", s.Code())
	}
}

func TestDBErr_Class_AndIs(t *testing.T) {
	tr := newT(t)
	if tr.Class(errDeadlock) != dberr.ClassDeadlock {
		t.Fatal("Class mismatch")
	}
	if !tr.Is(errDeadlock, dberr.ClassDeadlock) {
		t.Fatal("Is should be true")
	}
	if tr.Is(errUnique, dberr.ClassDeadlock) {
		t.Fatal("Is should be false")
	}
}

// ---- ErrorIsDriver 用标准库 database/sql + net 错误验证 ----

func TestDBErr_ErrorIsDriver_Stdlib(t *testing.T) {
	d := dberr.ErrorIsDriver{Rules: []dberr.ErrorIsRule{
		{Target: sql.ErrNoRows, Class: dberr.ClassNotFound},
		{Target: sql.ErrConnDone, Class: dberr.ClassConnection},
	}}
	if d.Classify(sql.ErrNoRows) != dberr.ClassNotFound {
		t.Fatal("ErrNoRows→NotFound")
	}
	if d.Classify(sql.ErrConnDone) != dberr.ClassConnection {
		t.Fatal("ErrConnDone→Connection")
	}
	// 包一层仍命中(errors.Is)。
	wrapped := fmt.Errorf("wrap: %w", sql.ErrNoRows)
	if d.Classify(wrapped) != dberr.ClassNotFound {
		t.Fatal("wrapped ErrNoRows should still classify")
	}
	if d.Classify(stderrors.New("unrelated")) != dberr.ClassUnknown {
		t.Fatal("unrelated→Unknown")
	}
}

// 模拟 net.Error 超时:用自定义类型满足 interface,ErrorIsDriver 无法识别
// (因为不是 errors.Is 命中),应归为 Unknown——证明 ErrorIsDriver 只认哨兵不认 interface。
type timeoutErr struct{ msg string }

func (e *timeoutErr) Error() string   { return e.msg }
func (e *timeoutErr) Timeout() bool   { return true }
func (e *timeoutErr) Temporary() bool { return true }

func TestDBErr_ErrorIsDriver_NotForInterface(t *testing.T) {
	d := dberr.ErrorIsDriver{Rules: []dberr.ErrorIsRule{
		{Target: sql.ErrNoRows, Class: dberr.ClassNotFound},
	}}
	te := &timeoutErr{msg: "i am a net.Error timeout"}
	if d.Classify(te) != dberr.ClassUnknown {
		t.Fatal("ErrorIsDriver should not match by interface, only by errors.Is")
	}
	_ = net.ErrClosed // 触发 net 包引用(避免未用 import)
	_ = time.Second
}

func TestDBErr_NoopDriver(t *testing.T) {
	d := dberr.NoopDriver{}
	if d.Classify(stderrors.New("x")) != dberr.ClassUnknown {
		t.Fatal("NoopDriver always Unknown")
	}
}
