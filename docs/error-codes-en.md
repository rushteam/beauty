# Structured Error Codes

`pkg/api/errors` provides a unified three-layer error system:

```
Business Code (e.g. 10404)
    ↓ auto-mapped
gRPC status code (codes.NotFound)
    ↓ auto-mapped
HTTP status code (404) + JSON body
```

Handlers only need to return `*errors.Status`; the recovery middleware and interceptors handle conversion automatically—no manual `w.WriteHeader` or `status.Error` required.

---

## Quick Start

```go
import apperrors "github.com/rushteam/beauty/pkg/api/errors"

// gRPC handler
func (s *UserSvc) GetUser(ctx context.Context, req *pb.GetUserReq) (*pb.User, error) {
    user, err := s.repo.Find(ctx, req.Id)
    if err != nil {
        return nil, apperrors.NotFound("user not found")
    }
    return user, nil
}

// HTTP handler (requires HTTPMiddlewareErrorHandler middleware)
func GetOrder(w http.ResponseWriter, r *http.Request) {
    order, err := svc.Find(r.Context(), id)
    if err != nil {
        apperrors.SetError(r.Context(), apperrors.NotFound("order not found"))
        return
    }
    json.NewEncoder(w).Encode(order)
}
```

HTTP response received by the client:

```json
HTTP/1.1 404 Not Found
Content-Type: application/json

{"code": 404, "message": "order not found"}
```

---

## Framework Integration

### gRPC Server

`grpcserver.New` mounts the recovery interceptor by default, which recognizes `*errors.Status` and converts it automatically. No extra configuration needed.

To explicitly mount the gRPC error conversion interceptor (without going through recovery):

```go
import (
    apperrors "github.com/rushteam/beauty/pkg/api/errors"
    "github.com/rushteam/beauty/pkg/service/grpcserver"
)

beauty.WithGrpcServer(":9090", register,
    grpcserver.WithGrpcServerUnaryInterceptor(
        apperrors.GRPCUnaryServerInterceptor,
    ),
    grpcserver.WithGrpcServerStreamInterceptor(
        apperrors.GRPCStreamServerInterceptor,
    ),
)
```

### HTTP Server

**Option 1 (recommended):** Add `HTTPMiddlewareErrorHandler` on top of the recovery middleware; use `SetError` inside handlers to write errors:

```go
import (
    apperrors "github.com/rushteam/beauty/pkg/api/errors"
    "github.com/rushteam/beauty/pkg/middleware/recovery"
)

beauty.WithWebServer(":8080", mux,
    webserver.WithMiddleware(recovery.HTTPMiddleware()),                    // catch panics
    webserver.WithMiddleware(apperrors.HTTPMiddlewareErrorHandler),         // convert errors to JSON
)

// inside handler
func MyHandler(w http.ResponseWriter, r *http.Request) {
    if err := validate(r); err != nil {
        apperrors.SetError(r.Context(), apperrors.InvalidArgument(err.Error()))
        return
    }
    // normal logic...
}
```

**Option 2:** Call `WriteHTTP` directly from the handler, suitable when the error type is known:

```go
func MyHandler(w http.ResponseWriter, r *http.Request) {
    user, err := svc.GetUser(r.Context(), id)
    if err != nil {
        if s, ok := apperrors.FromError(err); ok {
            apperrors.WriteHTTP(w, s)
        } else {
            apperrors.WriteHTTP(w, apperrors.Internal("unexpected error"))
        }
        return
    }
    json.NewEncoder(w).Encode(user)
}
```

---

## Built-in Error Codes

| Function | Code | HTTP | gRPC |
|------|------|------|------|
| `InvalidArgument(msg)` | 400 | 400 | INVALID_ARGUMENT |
| `Unauthenticated(msg)` | 401 | 401 | UNAUTHENTICATED |
| `Forbidden(msg)` | 403 | 403 | PERMISSION_DENIED |
| `NotFound(msg)` | 404 | 404 | NOT_FOUND |
| `Conflict(msg)` | 409 | 409 | ALREADY_EXISTS |
| `TooManyRequests(msg)` | 429 | 429 | RESOURCE_EXHAUSTED |
| `Internal(msg)` | 500 | 500 | INTERNAL |
| `Unimplemented(msg)` | 501 | 501 | UNIMPLEMENTED |
| `Unavailable(msg)` | 503 | 503 | UNAVAILABLE |

---

## Custom Business Error Codes

Business modules register codes in their own `init()`; the framework reserves 1–999, business codes start at 1000:

```go
// internal/errors/codes.go
package errors

import apperrors "github.com/rushteam/beauty/pkg/api/errors"

const (
    ErrUserNotFound  = apperrors.Code(10404)
    ErrUserExist     = apperrors.Code(10409)
    ErrOrderExpired  = apperrors.Code(20422)
)

func init() {
    apperrors.Register(ErrUserNotFound, 404, 5 /*NOT_FOUND*/,     "user not found")
    apperrors.Register(ErrUserExist,    409, 6 /*ALREADY_EXISTS*/, "user already exists")
    apperrors.Register(ErrOrderExpired, 422, 9 /*FAILED_PRECONDITION*/, "order expired")
}
```

Usage:

```go
return apperrors.New(ErrUserNotFound, "").
    WithDetail(&apperrors.ResourceInfo{ResourceType: "User", Name: userID})
```

Or use the `Code` value directly:

```go
return apperrors.Newf(errors.ErrUserNotFound, "user %s not found", userID)
```

---

## Structured Details

Details let callers understand what went wrong in a machine-readable way:

```go
// Validation failure — indicate which fields are invalid
return apperrors.InvalidArgument("request validation failed").
    WithDetail(&apperrors.FieldViolation{Field: "email", Description: "must be a valid email"}).
    WithDetail(&apperrors.FieldViolation{Field: "age",   Description: "must be >= 0"})

// Rate limiting — tell the client when to retry
return apperrors.TooManyRequests("rate limit exceeded").
    WithDetail(&apperrors.RetryInfo{RetryDelay: 5 * time.Second})

// Resource not found — indicate which resource
return apperrors.NotFound("resource not found").
    WithDetail(&apperrors.ResourceInfo{ResourceType: "Order", Name: orderID})

// Carry machine-readable reason and extended metadata
return apperrors.Forbidden("operation not allowed").
    WithDetail(&apperrors.ErrorInfo{
        Reason:   "INSUFFICIENT_PERMISSION",
        Domain:   "order-service",
        Metadata: map[string]string{"required_role": "admin"},
    })
```

JSON received by the client:

```json
{
  "code": 400,
  "message": "request validation failed",
  "details": [
    {"type": "FieldViolation", "data": {"Field": "email", "Description": "must be a valid email"}},
    {"type": "FieldViolation", "data": {"Field": "age",   "Description": "must be >= 0"}}
  ]
}
```

---

## Underlying Error (Cause)

`WithCause` records the underlying error; **for server-side logging only, never serialized to the client**:

```go
user, err := db.QueryUser(ctx, id)
if err != nil {
    return apperrors.Internal("failed to get user").WithCause(err)
    // err contents (e.g. SQL statements, connection info) will not appear in the response
}
```

---

## Error Inspection

```go
// Check by Code
if s, ok := apperrors.FromError(err); ok {
    switch s.Code() {
    case apperrors.CodeNotFound:
        // handle 404
    case apperrors.CodeUnauthenticated:
        // handle 401
    }
}

// errors.Is style (match by Code, ignore message)
notFoundSentinel := apperrors.New(apperrors.CodeNotFound, "")
if errors.Is(err, notFoundSentinel) {
    // err is a NotFound type
}

// Restore from gRPC error
if s, ok := apperrors.FromGRPCError(grpcErr); ok {
    // s.Code() is the framework Code
}
```

---

## Incremental Migration

Existing code returning `fmt.Errorf(...)` does not need to change immediately. When the recovery middleware cannot recognize `*Status`, it falls back to 500—the same behavior as before. Replace module by module:

```go
// before migration
return nil, fmt.Errorf("user not found: %w", err)  // → 500

// after migration
return nil, apperrors.NotFound("user not found").WithCause(err)  // → 404
```
