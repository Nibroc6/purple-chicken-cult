// Package notifyapi serves /api/notifications/*, /api/events (SSE) and notify-mute (plan §5.3).
//
// Owner: P0-W10 → P1-W05.
package notifyapi

import (
	"encoding/json"
	"io"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Nibroc6/purple-chicken-cult/internal/api"
	"github.com/Nibroc6/purple-chicken-cult/internal/api/apix"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate"
)

var (
	rGraph  = apix.Read("graph_read")
	wSocial = apix.Write("social")
)

// PingEvery is the SSE keep-alive comment period.
var PingEvery = 25 * time.Second

// ListOut is the answer to GET /api/notifications: a page plus the unread count.
type ListOut struct {
	Items  []gate.NotificationView `json:"items"`
	Next   string                  `json:"next,omitempty"`
	Unread int                     `json:"unread"`
}

// ReadIn is the body of POST /api/notifications/read.
type ReadIn struct {
	UpTo gate.Ref `json:"up_to"`
}

// Register registers the module routes on the /api router.
func Register(r chi.Router, d *api.Deps) {
	ns := d.Notify
	apix.Route(r, http.MethodGet, "/notifications", rGraph, func(w http.ResponseWriter, r *http.Request) error {
		page, err := ns.List(r.Context(), r.URL.Query().Get("cursor"))
		if err != nil {
			return err
		}
		n, err := ns.UnreadCount(r.Context())
		if err != nil {
			return err
		}
		return apix.JSON(w, http.StatusOK, ListOut{Items: page.Items, Next: page.Next, Unread: n})
	})
	apix.Route(r, http.MethodPost, "/notifications/read", apix.Free, func(w http.ResponseWriter, r *http.Request) error {
		in, err := apix.Decode[ReadIn](r, 0)
		if err != nil {
			return err
		}
		if err := ns.MarkRead(r.Context(), in.UpTo); err != nil {
			return err
		}
		return apix.NoContent(w)
	})
	mute := func(on bool) apix.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) error {
			if err := ns.MutePost(r.Context(), apix.Ref(r, "ref"), on); err != nil {
				return err
			}
			return apix.NoContent(w)
		}
	}
	apix.Route(r, http.MethodPut, "/posts/{ref}/notify-mute", wSocial, mute(true))
	apix.Route(r, http.MethodDelete, "/posts/{ref}/notify-mute", wSocial, mute(false))
	apix.Route(r, http.MethodGet, "/events", apix.Free, func(w http.ResponseWriter, r *http.Request) error {
		events, stop, err := ns.Stream(r.Context())
		if err != nil {
			return err
		}
		defer stop()
		rc := http.NewResponseController(w)
		// A stream outlives the server's read timeout; the request has no body.
		_ = rc.SetReadDeadline(time.Time{})
		_ = rc.SetWriteDeadline(time.Time{})
		h := w.Header()
		h.Set("Content-Type", "text/event-stream")
		h.Set("Cache-Control", "no-store")
		h.Set("X-Accel-Buffering", "no")
		w.WriteHeader(http.StatusOK)
		if err := rc.Flush(); err != nil {
			return nil
		}
		ping := time.NewTicker(PingEvery)
		defer ping.Stop()
		for {
			var frame string
			select {
			case <-r.Context().Done():
				return nil
			case <-ping.C:
				frame = ": ping\n\n"
			case ev, ok := <-events:
				if !ok {
					return nil // session ended
				}
				data, err := json.Marshal(ev.Data)
				if err != nil {
					return nil
				}
				frame = "event: " + ev.Type + "\ndata: " + string(data) + "\n\n"
			}
			if _, err := io.WriteString(w, frame); err != nil {
				return nil
			}
			if err := rc.Flush(); err != nil {
				return nil
			}
		}
	})
}
