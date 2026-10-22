// Package messagesapi serves direct messages: 1:1 conversations, group chats
// and posts shared into them (gate/dm).
//
// Owner: direct messages (contract request dm-1).
package messagesapi

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/Nibroc6/purple-chicken-cult/internal/api"
	"github.com/Nibroc6/purple-chicken-cult/internal/api/apix"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/dm"
)

var (
	rMsg    = apix.Read("messages")
	wMsg    = apix.Write("message")
	wGroup  = apix.Write("message_group")
	wSocial = apix.Write("social")
)

// DirectIn is POST /api/messages/direct.
type DirectIn struct {
	To gate.Ref `json:"to"`
}

// GroupIn is POST /api/messages/groups.
type GroupIn struct {
	Title   string     `json:"title"`
	Members []gate.Ref `json:"members"`
}

// RenameIn is PATCH /api/messages/{ref}.
type RenameIn struct {
	Title string `json:"title"`
}

// InviteIn is POST /api/messages/{ref}/members.
type InviteIn struct {
	People []gate.Ref `json:"people"`
}

// SendIn is POST /api/messages/{ref}/messages.
type SendIn struct {
	Body string   `json:"body"`
	Post gate.Ref `json:"post,omitempty"`
}

// ReadIn is POST /api/messages/{ref}/read.
type ReadIn struct {
	UpTo gate.Ref `json:"up_to"`
}

// UnreadOut is GET /api/messages/unread.
type UnreadOut struct {
	Unread int `json:"unread"`
}

// Register registers the module routes on the /api router.
func Register(r chi.Router, d *api.Deps) {
	s := d.DM
	conv := func(w http.ResponseWriter, v gate.ConversationView, err error) error {
		if err != nil {
			return err
		}
		return apix.JSON(w, http.StatusOK, v)
	}
	apix.Route(r, http.MethodGet, "/messages", rMsg, func(w http.ResponseWriter, r *http.Request) error {
		page, err := s.List(r.Context(), r.URL.Query().Get("cursor"))
		if err != nil {
			return err
		}
		return apix.JSON(w, http.StatusOK, page)
	})
	apix.Route(r, http.MethodGet, "/messages/unread", rMsg, func(w http.ResponseWriter, r *http.Request) error {
		n, err := s.Unread(r.Context())
		if err != nil {
			return err
		}
		return apix.JSON(w, http.StatusOK, UnreadOut{Unread: n})
	})
	apix.Route(r, http.MethodPost, "/messages/direct", wSocial, func(w http.ResponseWriter, r *http.Request) error {
		in, err := apix.Decode[DirectIn](r, 0)
		if err != nil {
			return err
		}
		v, err := s.Direct(r.Context(), in.To)
		return conv(w, v, err)
	})
	apix.Route(r, http.MethodPost, "/messages/groups", wGroup, func(w http.ResponseWriter, r *http.Request) error {
		in, err := apix.Decode[GroupIn](r, 0)
		if err != nil {
			return err
		}
		v, err := s.CreateGroup(r.Context(), in.Title, in.Members)
		return conv(w, v, err)
	})
	apix.Route(r, http.MethodGet, "/messages/{ref}", rMsg, func(w http.ResponseWriter, r *http.Request) error {
		v, err := s.Get(r.Context(), apix.Ref(r, "ref"))
		return conv(w, v, err)
	})
	apix.Route(r, http.MethodPatch, "/messages/{ref}", wGroup, func(w http.ResponseWriter, r *http.Request) error {
		in, err := apix.Decode[RenameIn](r, 0)
		if err != nil {
			return err
		}
		v, err := s.Rename(r.Context(), apix.Ref(r, "ref"), in.Title)
		return conv(w, v, err)
	})
	apix.Route(r, http.MethodDelete, "/messages/{ref}", wSocial, func(w http.ResponseWriter, r *http.Request) error {
		if err := s.Leave(r.Context(), apix.Ref(r, "ref")); err != nil {
			return err
		}
		return apix.NoContent(w)
	})
	apix.Route(r, http.MethodPost, "/messages/{ref}/join", wSocial, func(w http.ResponseWriter, r *http.Request) error {
		v, err := s.Join(r.Context(), apix.Ref(r, "ref"))
		return conv(w, v, err)
	})
	apix.Route(r, http.MethodPost, "/messages/{ref}/members", wGroup, func(w http.ResponseWriter, r *http.Request) error {
		in, err := apix.Decode[InviteIn](r, 0)
		if err != nil {
			return err
		}
		v, err := s.Invite(r.Context(), apix.Ref(r, "ref"), in.People)
		return conv(w, v, err)
	})
	apix.Route(r, http.MethodDelete, "/messages/{ref}/members/{id}", wSocial, func(w http.ResponseWriter, r *http.Request) error {
		if err := s.Remove(r.Context(), apix.Ref(r, "ref"), apix.Ref(r, "id")); err != nil {
			return err
		}
		return apix.NoContent(w)
	})
	mute := func(on bool) apix.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) error {
			if err := s.Mute(r.Context(), apix.Ref(r, "ref"), on); err != nil {
				return err
			}
			return apix.NoContent(w)
		}
	}
	apix.Route(r, http.MethodPut, "/messages/{ref}/mute", wSocial, mute(true))
	apix.Route(r, http.MethodDelete, "/messages/{ref}/mute", wSocial, mute(false))
	apix.Route(r, http.MethodGet, "/messages/{ref}/messages", rMsg, func(w http.ResponseWriter, r *http.Request) error {
		page, err := s.Messages(r.Context(), apix.Ref(r, "ref"), r.URL.Query().Get("cursor"))
		if err != nil {
			return err
		}
		return apix.JSON(w, http.StatusOK, page)
	})
	apix.Route(r, http.MethodPost, "/messages/{ref}/messages", wMsg, func(w http.ResponseWriter, r *http.Request) error {
		in, err := apix.Decode[SendIn](r, 32<<10)
		if err != nil {
			return err
		}
		m, err := s.Send(r.Context(), apix.Ref(r, "ref"), dm.Draft{Body: in.Body, Post: in.Post})
		if err != nil {
			return err
		}
		return apix.JSON(w, http.StatusCreated, m)
	})
	apix.Route(r, http.MethodDelete, "/messages/{ref}/messages/{id}", wSocial, func(w http.ResponseWriter, r *http.Request) error {
		if err := s.Delete(r.Context(), apix.Ref(r, "ref"), apix.Ref(r, "id")); err != nil {
			return err
		}
		return apix.NoContent(w)
	})
	apix.Route(r, http.MethodPost, "/messages/{ref}/read", apix.Free, func(w http.ResponseWriter, r *http.Request) error {
		in, err := apix.Decode[ReadIn](r, 0)
		if err != nil {
			return err
		}
		if err := s.MarkRead(r.Context(), apix.Ref(r, "ref"), in.UpTo); err != nil {
			return err
		}
		return apix.NoContent(w)
	})
}
