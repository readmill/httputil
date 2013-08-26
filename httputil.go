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

const CommonLogFmt = `%s - - [%s] "%s %s %s" %d %d "%s" "%s" %d`

var (
	LogFmt = CommonLogFmt
	notify = []chan *Access{}
)

func Notify(ch chan *Access) {
	notify = append(notify, ch)
}

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
}

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

type Handler struct {
	inner       http.Handler
	contentType string
	accept      string
	allow       []string
}

func (h *Handler) Accept(ctype string) {
	h.accept = ctype
}

func (h *Handler) Allow(methods ...string) {
	h.allow = methods
}

func NewHandler(inner http.Handler, ctype string) *Handler {
	return &Handler{
		inner:       inner,
		contentType: ctype,
	}
}

type ResponseWriter struct {
	StatusCode  int
	ContentType string
	inner       http.ResponseWriter
}

func (rw *ResponseWriter) HasStatus() bool {
	return rw.StatusCode != 0
}

func (rw *ResponseWriter) WriteHeader(status int) {
	rw.StatusCode = status
	rw.inner.WriteHeader(status)
}

func (rw *ResponseWriter) Write(b []byte) (int, error) {
	return rw.inner.Write(b)
}

func (rw *ResponseWriter) Header() http.Header {
	return rw.inner.Header()
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
		}
	}
}

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
	ctype := r.Header.Get("Content-Type")
	if ctype != "" && h.accept != "" && ctype != h.accept {
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

func Error(w http.ResponseWriter, err string, code int) {
	if rw, ok := w.(*ResponseWriter); ok {
		switch rw.ContentType {
		case "application/json":
			err = fmt.Sprintf(`{"error":%s}`, strconv.QuoteToASCII(err))
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

func init() {
	ch := make(chan *Access, 1)

	Notify(ch)

	go func() {
		for {
			log.Println((<-ch).String())
		}
	}()
}
