// Package hio provides a simple HTTP handler interface that allows chaining handlers.
package hio

import (
	"encoding/json/v2"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"slices"
	"strings"
)

// Handler is a chainable [http.Handler] implementation.
type Handler func(http.ResponseWriter, *http.Request) Handler

// ServeHTTP runs the [Handler] chain until one returns nil.
func (h Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if next := h(w, r); next != nil {
		next.ServeHTTP(w, r)
	}
}

// Middleware is an alias for a function that takes and returns an [http.Handler].
type Middleware = func(http.Handler) http.Handler

// Router adapts [http.ServeMux] to use [Handler] with an error logging [Responder].
type Router struct {
	m      *http.ServeMux
	r      Responder
	prefix string
	mws    []Middleware
}

// NewRouter returns a new [Router] that logs errors using the provided logger and function.
func NewRouter(l *slog.Logger, fn func(http.ResponseWriter, *http.Request, *slog.Logger, error)) *Router {
	return &Router{
		m: http.NewServeMux(),
		r: NewErrorLoggingResponder(l, fn),
	}
}

// ServeHTTP dispatches the request to the handler whose pattern most closely matches the request URL.
func (ro *Router) ServeHTTP(w http.ResponseWriter, r *http.Request) { ro.m.ServeHTTP(w, r) }

// Handle registers the handler for the given pattern and method responding with the [Responder] provided in [NewRouter].
func (ro *Router) Handle(method, pattern string, handler func(Responder) Handler) {
	ro.handle(strings.ToUpper(method), pattern, handler)
}

// Get registers a GET handler for the given pattern responding with the [Responder] provided in [NewRouter].
func (ro *Router) Get(pattern string, handler func(Responder) Handler) {
	ro.handle(http.MethodGet, pattern, handler)
}

// Post registers a POST handler for the given pattern responding with the [Responder] provided in [NewRouter].
func (ro *Router) Post(pattern string, handler func(Responder) Handler) {
	ro.handle(http.MethodPost, pattern, handler)
}

// Put registers a PUT handler for the given pattern responding with the [Responder] provided in [NewRouter].
func (ro *Router) Put(pattern string, handler func(Responder) Handler) {
	ro.handle(http.MethodPut, pattern, handler)
}

// Delete registers a DELETE handler for the given pattern responding with the [Responder] provided in [NewRouter].
func (ro *Router) Delete(pattern string, handler func(Responder) Handler) {
	ro.handle(http.MethodDelete, pattern, handler)
}

// Patch registers a PATCH handler for the given pattern responding with the [Responder] provided in [NewRouter].
func (ro *Router) Patch(pattern string, handler func(Responder) Handler) {
	ro.handle(http.MethodPatch, pattern, handler)
}

// Group creates a new [Router] with the given prefix and middlewares.
func (ro *Router) Group(prefix string, mws ...Middleware) *Router {
	r := &Router{
		m:      ro.m,
		r:      ro.r,
		prefix: ro.prefix + "/" + strings.Trim(prefix, "/"),
		mws:    make([]Middleware, len(ro.mws), len(ro.mws)+len(mws)),
	}

	copy(r.mws, ro.mws)

	r.mws = append(r.mws, mws...)

	return r
}

// Use adds the given middlewares to the [Router].
func (ro *Router) Use(mws ...Middleware) { ro.mws = append(ro.mws, mws...) }

func (ro *Router) wrap(h http.Handler) http.Handler {
	if len(ro.mws) > 0 {
		for _, mw := range slices.Backward(ro.mws) {
			h = mw(h)
		}
	}
	return h
}

func (ro *Router) handle(method, pattern string, handler func(Responder) Handler) {
	ro.m.Handle(method+" "+strings.TrimRight(ro.prefix+"/"+strings.Trim(pattern, "/"), "/"), ro.wrap(handler(ro.r)))
}

// Responder provides helpers to write HTTP responses.
type Responder struct{ err func(error) Handler }

// NewResponder returns a new [Responder].
// err is called when an error occurs during response writing.
func NewResponder(err func(error) Handler) Responder { return Responder{err: err} }

// NewErrorLoggingResponder returns a new [Responder] that logs errors using the provided logger and function.
func NewErrorLoggingResponder(l *slog.Logger, fn func(http.ResponseWriter, *http.Request, *slog.Logger, error)) Responder {
	return Responder{err: func(err error) Handler {
		return func(w http.ResponseWriter, r *http.Request) Handler {
			fn(w, r, l, err)
			return nil
		}
	}}
}

// Error responds with a formatted error message.
func (rs Responder) Error(format string, args ...any) Handler {
	return rs.err(fmt.Errorf(format, args...))
}

// Redirect diverts the request to the URL with the status code.
func (rs Responder) Redirect(code int, url string) Handler {
	return func(w http.ResponseWriter, r *http.Request) Handler {
		http.Redirect(w, r, url, code)
		return nil
	}
}

// Text writes a text response with the status code.
func (rs Responder) Text(code int, message string) Handler {
	return func(w http.ResponseWriter, r *http.Request) Handler {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(code)
		fmt.Fprint(w, message)
		return nil
	}
}

// JSON writes a JSON response with the status code.
func (rs Responder) JSON(code int, from any) Handler {
	data, err := json.Marshal(from)
	if err != nil {
		return rs.Error("encoding JSON: %w", err)
	}
	return func(w http.ResponseWriter, r *http.Request) Handler {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		w.Write(data)
		return nil
	}
}

// DecodeJSON reads and decodes JSON.
func DecodeJSON(from io.Reader, to any) error {
	if err := json.UnmarshalRead(from, to); err != nil {
		return fmt.Errorf("unmarshaling json: %w", err)
	}
	v, ok := to.(interface{ Validate() error })
	if ok {
		if err := v.Validate(); err != nil {
			return fmt.Errorf("validating: %w", err)
		}
	}
	return nil
}

// EncodeJSON writes JSON to the [http.ResponseWriter] with the status code.
func EncodeJSON(to http.ResponseWriter, from any, status int) error {
	to.Header().Set("Content-Type", "application/json")
	to.WriteHeader(status)
	return json.MarshalWrite(to, from)
}

// MaxBytesReader wraps [http.MaxBytesReader] to ensure the original [http.ResponseWriter] is unwrapped
// so that it can instruct the [http.Server] to disconnect clients when they reach the max bytes limit.
func MaxBytesReader(w http.ResponseWriter, rc io.ReadCloser, max int64) io.ReadCloser {
	type unwrapper interface {
		Unwrap() http.ResponseWriter
	}

	for {
		v, ok := w.(unwrapper)
		if !ok {
			break
		}
		w = v.Unwrap()
	}

	return http.MaxBytesReader(w, rc, max)
}

// TrailingSlashRedirector is a middleware that redirects requests with a trailing slash to the same URL without the trailing slash.
func TrailingSlashRedirector(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" && strings.HasSuffix(r.URL.Path, "/") {
			path := strings.TrimRight(r.URL.Path, "/")
			if r.URL.RawQuery != "" {
				path += "?" + r.URL.RawQuery
			}
			w.Header()["Content-Type"] = nil
			http.Redirect(w, r, path, http.StatusPermanentRedirect)
			return
		}
	})
}
