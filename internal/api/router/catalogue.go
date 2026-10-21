package router

import (
	"github.com/Nibroc6/purple-chicken-cult/internal/api/apix"
)

// Entry is one planned route (plan §5.3), relative to /api.
type Entry struct {
	Method, Pattern string
	Class           apix.Class
	Module          string
	Phase           int
}

var (
	free = apix.Free
	rF   = apix.Read("feed_page")
	rT   = apix.Read("tree_view")
	rG   = apix.Read("graph_read")
	rR   = apix.Read("post_read")
	wP   = apix.Write("post")
	wL   = apix.Write("like")
	wF   = apix.Write("friend_request")
	wR   = apix.Write("report")
	wS   = apix.Write("social")
	wA   = apix.Write("account")
	wI   = apix.Write("invite_handshake")
	wV   = apix.Write("save")
	wM   = apix.Write("media")
)

func e(mod string, phase int, method, pattern string, c apix.Class) Entry {
	return Entry{Method: method, Pattern: pattern, Class: c, Module: mod, Phase: phase}
}

// Catalogue is the complete planned route list (plan §5.3). router_test checks
// every registered route against it ("skip missing" until INT-0 flips it to
// "require all"). A new path is a contract-steward request.
var Catalogue = []Entry{
	// authapi (P0-W01 → P1-W07)
	e("authapi", 0, "GET", "/session", free),
	e("authapi", 0, "POST", "/auth/login/begin", apix.Anon("login")),
	e("authapi", 0, "POST", "/auth/login/finish", apix.Anon("login")),
	e("authapi", 0, "POST", "/auth/logout", free),
	e("authapi", 0, "POST", "/auth/stepup/begin", wA),
	e("authapi", 0, "POST", "/auth/stepup/finish", wA),
	e("authapi", 0, "GET", "/auth/passkeys", rG),
	e("authapi", 0, "POST", "/auth/passkeys/begin", wA),
	e("authapi", 0, "POST", "/auth/passkeys/finish", wA),
	e("authapi", 0, "DELETE", "/auth/passkeys/{id}", wA),
	e("authapi", 0, "POST", "/enroll/begin", apix.Anon("enroll")),
	e("authapi", 0, "POST", "/enroll/finish", apix.Anon("enroll")),
	e("authapi", 1, "POST", "/auth/dbsc/register", apix.Anon("dbsc")),
	e("authapi", 1, "POST", "/auth/dbsc/refresh", apix.Anon("dbsc")),

	// invitesapi (P0-W04 → P1-W02)
	e("invitesapi", 0, "GET", "/invites/budget", rG),
	e("invitesapi", 0, "GET", "/invites/invitees", rG),
	e("invitesapi", 0, "POST", "/invites/handshakes", wI),
	e("invitesapi", 0, "GET", "/invites/handshakes/{hs}/frame", free),
	e("invitesapi", 0, "GET", "/invites/handshakes/{hs}", free),
	e("invitesapi", 0, "POST", "/invites/handshakes/{hs}/confirm", wI),
	e("invitesapi", 0, "DELETE", "/invites/handshakes/{hs}", wI),
	e("invitesapi", 0, "POST", "/join/start", apix.Anon("join")),
	e("invitesapi", 0, "POST", "/join/claim", apix.Anon("join")),
	e("invitesapi", 0, "GET", "/join/status", apix.Anon("join")),
	e("invitesapi", 0, "POST", "/join/register/begin", apix.Anon("join")),
	e("invitesapi", 0, "POST", "/join/register/finish", apix.Anon("join")),
	e("invitesapi", 1, "POST", "/invites/handshakes/{hs}/epub", wI),
	e("invitesapi", 1, "GET", "/invites/handshakes/{hs}/peer", free),
	e("invitesapi", 1, "POST", "/join/audio/start", apix.Anon("join")),
	e("invitesapi", 1, "POST", "/invites/handshakes/{hs}/report", wI),
	e("invitesapi", 1, "POST", "/join/dual/frame", apix.Anon("join")),
	// steward: contract request p1-w02-1 (co-signing on both sides of a handshake)
	e("invitesapi", 1, "POST", "/join/register/cosign", apix.Anon("join")),
	e("invitesapi", 1, "POST", "/invites/handshakes/{hs}/cosign", wI),

	// friendsapi (P0-W03 → P1-W03)
	e("friendsapi", 0, "GET", "/friends", rG),
	e("friendsapi", 0, "GET", "/friends/requests", rG),
	e("friendsapi", 0, "POST", "/people/{ref}/request", wF),
	e("friendsapi", 0, "POST", "/friends/requests/{ref}/accept", wF),
	e("friendsapi", 0, "POST", "/friends/requests/{ref}/decline", wS),
	e("friendsapi", 0, "DELETE", "/people/{ref}/request", wS),
	e("friendsapi", 0, "DELETE", "/friends/{ref}", wS),
	e("friendsapi", 0, "PUT", "/people/{ref}/block", wS),
	e("friendsapi", 0, "DELETE", "/people/{ref}/block", wS),
	e("friendsapi", 0, "GET", "/blocks", rG),
	e("friendsapi", 0, "PUT", "/people/{ref}/mute", wS),
	e("friendsapi", 0, "DELETE", "/people/{ref}/mute", wS),
	e("friendsapi", 0, "GET", "/people/{ref}", rG),
	e("friendsapi", 0, "GET", "/people/{ref}/posts", rR),
	e("friendsapi", 0, "GET", "/suggestions", rG),
	e("friendsapi", 1, "PUT", "/people/{ref}/hide-via", wS),
	e("friendsapi", 1, "DELETE", "/people/{ref}/hide-via", wS),
	e("friendsapi", 1, "POST", "/friends/{ref}/upgrade", wI),

	// postsapi (P0-W05 → P1-W12)
	e("postsapi", 0, "POST", "/posts", wP),
	e("postsapi", 0, "POST", "/posts/thread", wP),
	e("postsapi", 0, "GET", "/posts/{ref}", rR),
	e("postsapi", 0, "PATCH", "/posts/{ref}", wP),
	e("postsapi", 0, "DELETE", "/posts/{ref}", wP),
	e("postsapi", 0, "PUT", "/posts/{ref}/ref-policy", wS),
	e("postsapi", 0, "POST", "/posts/{ref}/detach", wS),
	e("postsapi", 0, "POST", "/posts/{ref}/detach-all", wS),

	// engageapi (P0-W06 → P1-W08)
	e("engageapi", 0, "PUT", "/posts/{ref}/like", wL),
	e("engageapi", 0, "DELETE", "/posts/{ref}/like", wL),
	e("engageapi", 0, "GET", "/posts/{ref}/likes", rR),
	e("engageapi", 0, "POST", "/seen", free),
	e("engageapi", 0, "GET", "/seen/recent", rF),
	e("engageapi", 0, "POST", "/posts/{ref}/hide", wS),
	e("engageapi", 0, "POST", "/posts/{ref}/show-less", wS),
	e("engageapi", 1, "PUT", "/posts/{ref}/save", wV),
	e("engageapi", 1, "DELETE", "/posts/{ref}/save", wV),
	e("engageapi", 1, "GET", "/saves", rF),

	// feedapi (P0-W07 → P1-W08)
	e("feedapi", 0, "GET", "/feed", rF),
	e("feedapi", 0, "GET", "/feed/expand", rF),
	e("feedapi", 0, "GET", "/feed/new", rF),
	e("feedapi", 0, "GET", "/posts/{ref}/why", rR),

	// treeapi (P0-W08 → P1-W08)
	e("treeapi", 0, "GET", "/tree/{ref}", rT),
	e("treeapi", 0, "GET", "/tree/s/{snapshot}/row", rT),
	e("treeapi", 0, "GET", "/tree/s/{snapshot}/more", rT),

	// notifyapi (P0-W10 → P1-W05)
	e("notifyapi", 0, "GET", "/notifications", rG),
	e("notifyapi", 0, "POST", "/notifications/read", free),
	e("notifyapi", 0, "PUT", "/posts/{ref}/notify-mute", wS),
	e("notifyapi", 0, "DELETE", "/posts/{ref}/notify-mute", wS),
	e("notifyapi", 0, "GET", "/events", free),

	// pushapi (P1-W05)
	e("pushapi", 1, "GET", "/push/key", free),
	e("pushapi", 1, "GET", "/push/subscriptions", free), // p1-w05-1: the caller's state of one endpoint
	e("pushapi", 1, "POST", "/push/subscriptions", wA),
	e("pushapi", 1, "DELETE", "/push/subscriptions", wA),

	// accountapi (P0-W11 → P1-W10)
	e("accountapi", 0, "GET", "/me", free),
	e("accountapi", 0, "PATCH", "/me/settings", wA),
	e("accountapi", 0, "POST", "/me/export", wA),
	e("accountapi", 0, "POST", "/me/delete", wA),
	e("accountapi", 0, "GET", "/me/recovery", free),
	e("accountapi", 0, "POST", "/me/recovery/cancel", wA),
	e("accountapi", 0, "GET", "/keys", rG),
	e("accountapi", 0, "POST", "/mod/recovery", wA),
	e("accountapi", 0, "POST", "/mod/recovery/{id}/approve", wA),
	e("accountapi", 0, "GET", "/mod/recovery/{id}/link", wA),
	e("accountapi", 1, "POST", "/recovery/sessions", wA),
	e("accountapi", 1, "POST", "/recovery/claim", apix.Anon("recovery")),
	e("accountapi", 1, "GET", "/recovery/requests", rG),
	e("accountapi", 1, "POST", "/recovery/requests/{id}/approve", wA),
	e("accountapi", 1, "GET", "/recovery/status", apix.Anon("recovery")),

	// modapi (P0-W12 → P1-W06)
	e("modapi", 0, "POST", "/reports", wR),
	e("modapi", 0, "GET", "/blame", rG),
	e("modapi", 0, "GET", "/mod/queue", rG),
	e("modapi", 1, "POST", "/mod/items/{id}/claim", wS),
	e("modapi", 0, "POST", "/mod/items/{id}/resolve", wS),
	e("modapi", 0, "POST", "/mod/items/{id}/confirm", wS),
	e("modapi", 0, "GET", "/mod/log", rG),
	e("modapi", 0, "POST", "/csp-report", apix.Anon("csp_report")),
	e("modapi", 1, "POST", "/mod/quarantine", wS),
	e("modapi", 1, "POST", "/mod/items/{id}/csam-report", wS),

	// themeapi (P1-W09)
	e("themeapi", 1, "GET", "/me/theme", free), // steward: contract request p1-w09-1
	e("themeapi", 1, "PUT", "/me/theme", wA),
	e("themeapi", 1, "DELETE", "/me/theme", wA),

	// mediaapi (P1-W04)
	e("mediaapi", 1, "POST", "/media", wM),
	e("mediaapi", 1, "GET", "/media/{ref}/url", rR),

	// signalsapi (P1-W07)
	e("signalsapi", 1, "POST", "/signals", free),

	// cosignapi (P1-W01)
	e("cosignapi", 1, "POST", "/cosign", wA),

	// testhooks (SCAFFOLD; PCC_ENV=test && PCC_TEST_HOOKS=1 only)
	e("testhooks", 0, "POST", "/_test/reset", free),
	e("testhooks", 0, "POST", "/_test/clock", free),
	e("testhooks", 0, "POST", "/_test/seed", free),
	e("testhooks", 0, "POST", "/_test/login", free),
	e("testhooks", 0, "POST", "/_test/genesis", free),
}
