package httputil

import (
	"fmt"
	"log"
	"net"
	"net/http"
	"runtime/debug"
	"time"
)

var (
	CommonLogFmt = `%s - - [%s] "%s %s %s" %d %d "%s" "%s" %dms`
	LogFmt       = CommonLogFmt
	notify       = []chan *Access{}
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

type baseHandler struct {
	inner http.Handler
}

type responseWriter struct {
	StatusCode int
	inner      http.ResponseWriter
}

func (rw *responseWriter) HasStatus() bool {
	return rw.StatusCode != 0
}

func (rw *responseWriter) WriteHeader(status int) {
	rw.StatusCode = status
	rw.inner.WriteHeader(status)
}

func (rw *responseWriter) Write(b []byte) (int, error) {
	return rw.inner.Write(b)
}

func (rw *responseWriter) Header() http.Header {
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

func (h *baseHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var delta time.Duration

	rw := &responseWriter{inner: w}

	defer func() {
		if e := recover(); e != nil {
			if !rw.HasStatus() {
				rw.WriteHeader(http.StatusInternalServerError)
			}
			log.Printf("panic: %v", e)
			log.Println(debug.Stack())
		}
		if rw.HasStatus() {
			logRequest(r, rw.StatusCode, delta)
		} else {
			logRequest(r, http.StatusOK, delta)
		}
	}()

	t := time.Now()
	h.inner.ServeHTTP(rw, r)
	delta = time.Since(t)
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
