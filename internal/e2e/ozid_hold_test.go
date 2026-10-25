package e2e_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
)

// responseHold delays one call of the API origin on purpose, so that a browser scenario can make a
// request or its response arrive late, exactly when it chooses:
//
//   - phase "request": the front holds the request before it reaches the BFF (a late request);
//   - phase "response": the BFF handles the request at once and the front holds the answer,
//     Set-Cookie included, until released (a late response). The browser applies that
//     Set-Cookie whenever it arrives, whatever the page that asked has meanwhile become;
//   - phase "drop": the BFF handles the request and the front then aborts the connection, so the
//     browser never sees the answer, Set-Cookie included (a lost response).
//
// It arms one call per method name at a time. It takes one matching call: the first, or, with
// skip=N, the one after the first N.
type responseHold struct {
	mu    sync.Mutex
	armed map[string]*heldCall
}

type heldCall struct {
	phase   string
	skip    int
	taken   bool
	reached chan struct{}
	release chan struct{}
}

func newResponseHold() *responseHold { return &responseHold{armed: map[string]*heldCall{}} }

func (h *responseHold) arm(method, phase string, skip int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.armed[method] = &heldCall{phase: phase, skip: skip, reached: make(chan struct{}), release: make(chan struct{})}
}

func (h *responseHold) take(method string) *heldCall {
	h.mu.Lock()
	defer h.mu.Unlock()
	c := h.armed[method]
	if c == nil || c.taken {
		return nil
	}
	if c.skip > 0 {
		c.skip--
		return nil
	}
	c.taken = true
	return c
}

// serve forwards the call to next, holding it when it is the armed one.
func (h *responseHold) serve(next http.Handler, w http.ResponseWriter, r *http.Request) {
	method := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
	if r.Method == http.MethodOptions { // a preflight is never the held call
		next.ServeHTTP(w, r)
		return
	}
	c := h.take(method)
	if c == nil {
		next.ServeHTTP(w, r)
		return
	}
	if c.phase == "request" {
		close(c.reached)
		select {
		case <-c.release:
		case <-r.Context().Done():
			return
		}
		next.ServeHTTP(w, r)
		return
	}
	rec := httptest.NewRecorder()
	next.ServeHTTP(rec, r)
	close(c.reached)
	if c.phase == "drop" {
		panic(http.ErrAbortHandler) // the answer never leaves the front
	}
	select {
	case <-c.release:
	case <-r.Context().Done():
		return
	}
	for k, v := range rec.Header() {
		w.Header()[k] = v
	}
	w.WriteHeader(rec.Code)
	_, _ = w.Write(rec.Body.Bytes())
}

// mount adds the scenario's switches to the control origin: /hold?method=&phase=[&skip=N],
// /reached?method= ({"reached": true} once the armed call is held) and /release?method=.
func (h *responseHold) mount(mux *http.ServeMux) {
	mux.HandleFunc("/hold", func(w http.ResponseWriter, r *http.Request) {
		skip, _ := strconv.Atoi(r.URL.Query().Get("skip"))
		h.arm(r.URL.Query().Get("method"), r.URL.Query().Get("phase"), skip)
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("/reached", func(w http.ResponseWriter, r *http.Request) {
		h.mu.Lock()
		c := h.armed[r.URL.Query().Get("method")]
		h.mu.Unlock()
		got := false
		if c != nil {
			select {
			case <-c.reached:
				got = true
			default:
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]bool{"reached": got})
	})
	mux.HandleFunc("/release", func(w http.ResponseWriter, r *http.Request) {
		h.mu.Lock()
		c := h.armed[r.URL.Query().Get("method")]
		h.mu.Unlock()
		if c != nil {
			select {
			case <-c.release:
			default:
				close(c.release)
			}
		}
		w.WriteHeader(http.StatusNoContent)
	})
}
