// Package httputil provides HTTP utility functions as well as its own
// http.Handler to complement the "net/http" and "net/http/httputil"
// packages found in the standard library.
package httputil

import (
	"fmt"
	"html"
	"log"
	"net"
	"net/http"
	"runtime/debug"
	"strconv"
	"strings"
	"time"
)

// Common Log Format
// See <http://httpd.apache.org/docs/1.3/logs.html#common>
const CommonLogFmt = `%s - - [%s] "%s %s %s" %d %d "%s" "%s" %d`

var (
	LogFmt = CommonLogFmt // Log format to use
	notify = []chan *Access{}
)

// Notify sends all HTTP access events to the specified channel.
func Notify(ch chan *Access) {
	notify = append(notify, ch)
}

// Access represents a single HTTP access event (an answered request).
type Access struct {
	RemoteAddr    string
	Time          time.Time
	Method        string
	RequestURI    string
	Proto         string
	StatusCode    int
	ContentLength int64
	Referer       string
	UserAgent     string
	Duration      time.Duration
	Request       *http.Request
}

// String returns the string representation of an *Access according
// to LogFmt.
func (a *Access) String() string {
	return fmt.Sprintf(LogFmt,
		a.RemoteAddr,
		a.Time.Format("02/Jan/2006:15:04:05 -0700"),
		a.Method,
		a.RequestURI,
		a.Proto,
		a.StatusCode,
		a.ContentLength,
		a.Referer,
		a.UserAgent,
		a.Duration/time.Millisecond)
}

// Handler implements http.Handler.
type Handler struct {
	inner       http.Handler
	contentType string
	accept      string
	allow       []string
}

// NewHandler returns a new Handler which wraps the given http.Handler.
func NewHandler(inner http.Handler, ctype string) *Handler {
	return &Handler{
		inner:       inner,
		contentType: ctype,
	}
}

// Accept instructs the handler to only fulfill requests
// which have an Accept header of the given MIME type.
// Otherwise, a 406 Not Acceptable is returned.
func (h *Handler) Accept(mime string) {
	h.accept = mime
}

// Allow instructs the handler to only fulfill requests
// which use one of the given HTTP methods.
// Otherwise, a 405 Method Not Allowed is returned.
func (h *Handler) Allow(methods ...string) {
	h.allow = methods
}

// ResponseWriter wraps an http.ResponseWriter with
// additional capabilities.
type ResponseWriter struct {
	StatusCode  int
	ContentType string
	inner       http.ResponseWriter
}

// HasStatus returns whether or not the ResponseWriter has
// a status.
func (rw *ResponseWriter) HasStatus() bool {
	return rw.StatusCode != 0
}

// WriteHeader wraps (*http.ResponseWriter).WriteHeader and
// records the given status.
func (rw *ResponseWriter) WriteHeader(status int) {
	rw.StatusCode = status
	rw.inner.WriteHeader(status)
}

// Write wraps (*http.ResponseWriter).Write.
func (rw *ResponseWriter) Write(b []byte) (int, error) {
	return rw.inner.Write(b)
}

// Write wraps (*http.ResponseWriter).Header.
func (rw *ResponseWriter) Header() http.Header {
	return rw.inner.Header()
}

// Error wraps http.Error by writing appropriate error messages
// depending on the handler's Content-Type.
//
// For example, with the Content-Type set to application/json
// and the error string "oops!", the response body would be
// `{"error":"oops!"}`
func Error(w http.ResponseWriter, err string, code int) {
	if rw, ok := w.(*ResponseWriter); ok {
		switch rw.ContentType {
		case "application/json":
			err = fmt.Sprintf(`{"error":%s,"status":%d}`, strconv.QuoteToASCII(err), code)
		case "text/html":
			err = html.EscapeString(err)
		case "text/plain":
			fallthrough
		default:
			err = "error: " + err
		}
	}
	http.Error(w, err, code)
}

func logRequest(r *http.Request, statusCode int, delta time.Duration) {
	var referer, remoteAddr, userAgent string

	if h, ok := r.Header["Referer"]; ok {
		referer = h[0]
	} else {
		referer = "-"
	}

	if h, ok := r.Header["X-Forwarded-For"]; ok {
		remoteAddr = h[0]
	} else {
		if host, _, err := net.SplitHostPort(r.RemoteAddr); err != nil {
			remoteAddr = "?"
		} else {
			remoteAddr = host
		}
	}

	if h, ok := r.Header["User-Agent"]; ok {
		userAgent = h[0]
	} else {
		userAgent = "-"
	}

	for _, ch := range notify {
		ch <- &Access{
			remoteAddr,
			time.Now(),
			r.Method,
			r.RequestURI,
			r.Proto,
			statusCode,
			r.ContentLength,
			referer,
			userAgent,
			delta,
			r,
		}
	}
}

// ServeHTTP serves an HTTP request by calling the underlying http.Handler.
// Request duration and status are logged.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var delta time.Duration

	rw := &ResponseWriter{inner: w, ContentType: h.contentType}
	rw.Header().Set("Content-Type", h.contentType)

	defer func() {
		if e := recover(); e != nil {
			if !rw.HasStatus() {
				rw.WriteHeader(http.StatusInternalServerError)
			}
			log.Printf("panic: %v", e)
			debug.PrintStack()
		}
		if rw.HasStatus() {
			logRequest(r, rw.StatusCode, delta)
		} else {
			logRequest(r, http.StatusOK, delta)
		}
	}()

	t := time.Now()
	h.serveRequest(rw, r)
	delta = time.Since(t)
}

func (h *Handler) serveRequest(w http.ResponseWriter, r *http.Request) {
	mime := r.Header.Get("Accept")
	if mime != "" && mime != "*/*" && h.accept != "" && mime != h.accept {
		w.Header().Set("Accept", h.accept)
		w.WriteHeader(http.StatusNotAcceptable)
		return
	}

	if h.allow != nil {
		allowed := false

		for _, m := range h.allow {
			if r.Method == m {
				allowed = true
				break
			}
		}
		if !allowed {
			w.Header().Set("Allow", strings.Join(h.allow, ", "))
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
	}
	h.inner.ServeHTTP(w, r)
}

func init() {
	ch := make(chan *Access, 1)

	Notify(ch)

	go func() {
		for {
			log.Println((<-ch).String())
		}
	}()
}
