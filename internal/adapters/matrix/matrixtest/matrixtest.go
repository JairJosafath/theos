// Package matrixtest is a fake Matrix homeserver over httptest, serving the
// client-server API calls the matrix adapter makes: whoami, joined rooms,
// aliases, room and Space creation, state, invites, sends with transaction
// IDs, and /sync with since tokens; and a rate limit on demand. The
// adapter's tests and the controllers' envtest share it.
package matrixtest

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// ServerName is the fake homeserver's name: user IDs are @name:ServerName.
const ServerName = "theos.test"

// The keys and values the fake reads and writes more than once.
const (
	keyEventID   = "event_id"
	typeMember   = "m.room.member"
	memberInvite = "invite"
	memberJoin   = "join"
)

// Server is a fake homeserver.
type Server struct {
	srv *httptest.Server

	mu       sync.Mutex
	tokens   map[string]string // token → user ID
	rooms    map[string]*room  // room ID → room
	aliases  map[string]string // #alias:server → room ID
	txns     map[string]string // user/room/txn → event ID
	events   []*event          // every timeline event, by stream position
	next     int               // numbers rooms and events
	calls    map[string]int
	limited  int   // how many next calls answer 429
	limitFor int64 // the retry_after_ms they name
}

type room struct {
	id, creator string
	space       bool
	state       map[stateKey]map[string]any
}

type stateKey struct{ typ, key string }

type event struct {
	pos     int
	room    string
	id      string
	typ     string
	sender  string
	ts      int64
	content map[string]any
}

// New starts a fake homeserver, closed when t ends.
func New(t testing.TB) *Server {
	s := Start()
	t.Cleanup(s.Close)
	return s
}

// Start starts a fake homeserver the caller closes with Close: for a suite
// without a testing.TB, such as Ginkgo's.
func Start() *Server {
	s := &Server{tokens: map[string]string{}, rooms: map[string]*room{}, aliases: map[string]string{},
		txns: map[string]string{}, calls: map[string]int{}}
	s.srv = httptest.NewServer(http.HandlerFunc(s.serve))
	return s
}

// Close stops the server.
func (s *Server) Close() { s.srv.Close() }

// URL is the homeserver's address.
func (s *Server) URL() string { return s.srv.URL }

// NewUser makes the account @localpart:ServerName and returns its user ID
// and a fresh access token.
func (s *Server) NewUser(localpart string) (userID, token string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	userID, token = "@"+localpart+":"+ServerName, rand.Text()
	s.tokens[token] = userID
	return userID, token
}

// RateLimit makes the next n calls answer 429 with retry_after_ms ms.
func (s *Server) RateLimit(n int, ms int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.limited, s.limitFor = n, ms
}

// Calls is how many calls of name were answered, 429s left out: createRoom,
// invite, joined_rooms, whoami, directory, state.get, state.put, send.<type>,
// sync.
func (s *Server) Calls(name string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls[name]
}

// CreateRoomAs makes a room of user's holding alias (a localpart), as
// someone else on the homeserver would.
func (s *Server) CreateRoomAs(user, alias string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	r := s.newRoom(user, false)
	if alias != "" {
		s.aliases["#"+alias+":"+ServerName] = r.id
	}
	return r.id
}

// Join makes user, invited or not, a member of room, as accepting an invite.
func (s *Server) Join(roomID, user string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.setMember(s.rooms[roomID], user, user, memberJoin)
}

// Leave makes user leave room, as a bot kicked out.
func (s *Server) Leave(roomID, user string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.setMember(s.rooms[roomID], user, user, "leave")
}

// Post posts a person's m.text message in room, in the thread of
// threadRoot when it isn't "", and returns its event ID.
func (s *Server) Post(roomID, sender, body, threadRoot string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	content := map[string]any{"msgtype": "m.text", "body": body}
	if threadRoot != "" {
		content["m.relates_to"] = map[string]any{"rel_type": "m.thread", keyEventID: threadRoot}
	}
	return s.addEvent(roomID, "m.room.message", sender, content).id
}

// Room is a snapshot of a room.
type Room struct {
	ID, Creator string
	Name, Topic string
	// Alias is the alias pointing to the room, "" for none.
	Alias string
	Space bool
	// Children are the rooms a Space lists; Parents the Spaces a room names.
	Children, Parents []string
	// Members are the room's users and their membership: join or invite.
	Members map[string]string
	// Encrypted says the room has m.room.encryption.
	Encrypted bool
	// JoinRule is the room's join rule: invite for a private room.
	JoinRule string
	// Events are the room's timeline events, oldest first.
	Events []Event
}

// Event is a timeline event.
type Event struct {
	ID, Type, Sender string
	Content          map[string]any
}

// Room is room ID's snapshot, false when there is none.
func (s *Server) Room(id string) (Room, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.rooms[id]
	if !ok {
		return Room{}, false
	}
	out := Room{ID: r.id, Creator: r.creator, Space: r.space, Members: map[string]string{}}
	out.Name, _ = r.state[stateKey{"m.room.name", ""}]["name"].(string)
	out.Topic, _ = r.state[stateKey{"m.room.topic", ""}]["topic"].(string)
	out.JoinRule, _ = r.state[stateKey{"m.room.join_rules", ""}]["join_rule"].(string)
	_, out.Encrypted = r.state[stateKey{"m.room.encryption", ""}]
	for alias, id := range s.aliases {
		if id == r.id {
			out.Alias = alias
		}
	}
	for k, c := range r.state {
		switch {
		case k.typ == "m.space.child" && len(c) > 0:
			out.Children = append(out.Children, k.key)
		case k.typ == "m.space.parent" && len(c) > 0:
			out.Parents = append(out.Parents, k.key)
		case k.typ == typeMember:
			if m, _ := c["membership"].(string); m == memberJoin || m == memberInvite {
				out.Members[k.key] = m
			}
		}
	}
	slices.Sort(out.Children)
	slices.Sort(out.Parents)
	for _, e := range s.events {
		if e.room == r.id {
			out.Events = append(out.Events, Event{ID: e.id, Type: e.typ, Sender: e.sender, Content: e.content})
		}
	}
	return out, true
}

// RoomByAlias is the snapshot of the room alias (#localpart:server) points to.
func (s *Server) RoomByAlias(alias string) (Room, bool) {
	s.mu.Lock()
	id, ok := s.aliases[alias]
	s.mu.Unlock()
	if !ok {
		return Room{}, false
	}
	return s.Room(id)
}

// Rooms is how many rooms the homeserver has.
func (s *Server) Rooms() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.rooms)
}

// newRoom makes a room with creator in it. The caller holds mu.
func (s *Server) newRoom(creator string, space bool) *room {
	s.next++
	r := &room{id: fmt.Sprintf("!r%d:%s", s.next, ServerName), creator: creator, space: space,
		state: map[stateKey]map[string]any{}}
	s.rooms[r.id] = r
	s.setMember(r, creator, creator, memberJoin)
	return r
}

// setMember sets user's membership of r, as sender. The caller holds mu.
func (s *Server) setMember(r *room, sender, user, membership string) {
	content := map[string]any{"membership": membership}
	r.state[stateKey{typeMember, user}] = content
	s.addEvent(r.id, typeMember, sender, content)
}

// addEvent appends an event to room's timeline. The caller holds mu.
func (s *Server) addEvent(roomID, typ, sender string, content map[string]any) *event {
	s.next++
	e := &event{pos: len(s.events) + 1, room: roomID, id: fmt.Sprintf("$e%d", s.next), typ: typ, sender: sender,
		ts: time.Now().UnixMilli(), content: content}
	s.events = append(s.events, e)
	return e
}

// membership is user's membership of r. The caller holds mu.
func (r *room) membership(user string) string {
	m, _ := r.state[stateKey{typeMember, user}]["membership"].(string)
	return m
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func matrixError(w http.ResponseWriter, status int, code, msg string) {
	writeJSON(w, status, map[string]string{"errcode": code, "error": msg})
}

// serve answers one call.
func (s *Server) serve(w http.ResponseWriter, r *http.Request) {
	const prefix = "/_matrix/client/v3/"
	escaped := r.URL.EscapedPath()
	if !strings.HasPrefix(escaped, prefix) {
		matrixError(w, http.StatusNotFound, "M_UNRECOGNIZED", "not the client-server API")
		return
	}
	var parts []string
	for p := range strings.SplitSeq(strings.TrimPrefix(escaped, prefix), "/") {
		u, err := url.PathUnescape(p)
		if err != nil {
			matrixError(w, http.StatusBadRequest, "M_INVALID_PARAM", err.Error())
			return
		}
		parts = append(parts, u)
	}
	s.mu.Lock()
	if s.limited > 0 {
		s.limited--
		ms := s.limitFor
		s.mu.Unlock()
		writeJSON(w, http.StatusTooManyRequests, map[string]any{"errcode": "M_LIMIT_EXCEEDED",
			"error": "Too many requests", "retry_after_ms": ms})
		return
	}
	user, ok := s.tokens[strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")]
	s.mu.Unlock()
	if !ok {
		matrixError(w, http.StatusUnauthorized, "M_UNKNOWN_TOKEN", "Unknown access token")
		return
	}
	var body map[string]any
	if r.Body != nil {
		data, _ := io.ReadAll(r.Body)
		if len(data) > 0 && json.Unmarshal(data, &body) != nil {
			matrixError(w, http.StatusBadRequest, "M_NOT_JSON", "Content not JSON")
			return
		}
	}
	if parts[0] == "sync" && r.Method == http.MethodGet {
		s.sync(w, r, user)
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	switch {
	case r.Method == http.MethodGet && len(parts) == 2 && parts[0] == "account" && parts[1] == "whoami":
		s.calls["whoami"]++
		writeJSON(w, http.StatusOK, map[string]string{"user_id": user})
	case r.Method == http.MethodGet && len(parts) == 1 && parts[0] == "joined_rooms":
		s.calls["joined_rooms"]++
		joined := []string{}
		for id, rm := range s.rooms {
			if rm.membership(user) == memberJoin {
				joined = append(joined, id)
			}
		}
		slices.Sort(joined)
		writeJSON(w, http.StatusOK, map[string]any{"joined_rooms": joined})
	case r.Method == http.MethodGet && len(parts) == 3 && parts[0] == "directory" && parts[1] == "room":
		s.calls["directory"]++
		id, ok := s.aliases[parts[2]]
		if !ok {
			matrixError(w, http.StatusNotFound, "M_NOT_FOUND", "Room alias not found")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"room_id": id, "servers": []string{ServerName}})
	case r.Method == http.MethodPost && len(parts) == 1 && parts[0] == "createRoom":
		s.createRoom(w, user, body)
	case len(parts) >= 3 && parts[0] == "rooms":
		s.roomCall(w, r.Method, user, parts[1], parts[2:], body)
	default:
		matrixError(w, http.StatusNotFound, "M_UNRECOGNIZED", "Unrecognized request")
	}
}

// createRoom answers POST /createRoom. The caller holds mu.
func (s *Server) createRoom(w http.ResponseWriter, user string, body map[string]any) {
	alias := ""
	if name, _ := body["room_alias_name"].(string); name != "" {
		alias = "#" + name + ":" + ServerName
		if _, taken := s.aliases[alias]; taken {
			matrixError(w, http.StatusBadRequest, "M_ROOM_IN_USE", "Room alias already taken")
			return
		}
	}
	s.calls["createRoom"]++
	creation, _ := body["creation_content"].(map[string]any)
	r := s.newRoom(user, creation["type"] == "m.space")
	joinRule := "public"
	if body["preset"] == "private_chat" || body["preset"] == "trusted_private_chat" {
		joinRule = memberInvite
	}
	r.state[stateKey{"m.room.join_rules", ""}] = map[string]any{"join_rule": joinRule}
	if name, _ := body["name"].(string); name != "" {
		r.state[stateKey{"m.room.name", ""}] = map[string]any{"name": name}
	}
	if topic, _ := body["topic"].(string); topic != "" {
		r.state[stateKey{"m.room.topic", ""}] = map[string]any{"topic": topic}
	}
	if states, ok := body["initial_state"].([]any); ok {
		for _, st := range states {
			m, _ := st.(map[string]any)
			typ, _ := m["type"].(string)
			key, _ := m["state_key"].(string)
			content, _ := m["content"].(map[string]any)
			r.state[stateKey{typ, key}] = content
		}
	}
	if alias != "" {
		s.aliases[alias] = r.id
	}
	writeJSON(w, http.StatusOK, map[string]string{"room_id": r.id})
}

// roomCall answers /rooms/{room}/... The caller holds mu.
func (s *Server) roomCall(w http.ResponseWriter, method, user, roomID string, rest []string, body map[string]any) {
	r, ok := s.rooms[roomID]
	if !ok || r.membership(user) != memberJoin {
		matrixError(w, http.StatusForbidden, "M_FORBIDDEN", "You are not in this room")
		return
	}
	switch {
	case rest[0] == "state" && len(rest) >= 2:
		key := ""
		if len(rest) >= 3 {
			key = strings.Join(rest[2:], "/")
		}
		k := stateKey{rest[1], key}
		if method == http.MethodGet {
			s.calls["state.get"]++
			content, ok := r.state[k]
			if !ok {
				matrixError(w, http.StatusNotFound, "M_NOT_FOUND", "Event not found")
				return
			}
			writeJSON(w, http.StatusOK, content)
			return
		}
		if method != http.MethodPut {
			break
		}
		s.calls["state.put"]++
		if body == nil {
			body = map[string]any{}
		}
		r.state[k] = body
		writeJSON(w, http.StatusOK, map[string]string{keyEventID: s.addEvent(r.id, k.typ, user, body).id})
		return
	case rest[0] == memberInvite && method == http.MethodPost:
		target, _ := body["user_id"].(string)
		if m := r.membership(target); m == memberJoin || m == memberInvite {
			matrixError(w, http.StatusForbidden, "M_FORBIDDEN", target+" is already in the room.")
			return
		}
		s.calls[memberInvite]++
		s.setMember(r, user, target, memberInvite)
		writeJSON(w, http.StatusOK, map[string]any{})
		return
	case rest[0] == "send" && len(rest) == 3 && method == http.MethodPut:
		txn := user + "/" + r.id + "/" + rest[2]
		if id, ok := s.txns[txn]; ok {
			writeJSON(w, http.StatusOK, map[string]string{keyEventID: id})
			return
		}
		s.calls["send."+rest[1]]++
		e := s.addEvent(r.id, rest[1], user, body)
		s.txns[txn] = e.id
		writeJSON(w, http.StatusOK, map[string]string{keyEventID: e.id})
		return
	}
	matrixError(w, http.StatusNotFound, "M_UNRECOGNIZED", "Unrecognized request")
}

// sync answers GET /sync: the timeline events after since in the rooms user
// is in, waiting up to timeout for one. A since token is "s" and a stream
// position.
func (s *Server) sync(w http.ResponseWriter, r *http.Request, user string) {
	since := 0
	if tok := r.URL.Query().Get("since"); tok != "" {
		n, err := strconv.Atoi(strings.TrimPrefix(tok, "s"))
		if err != nil || !strings.HasPrefix(tok, "s") {
			matrixError(w, http.StatusBadRequest, "M_INVALID_PARAM", "bad since token")
			return
		}
		since = n
	}
	timeout, _ := strconv.Atoi(r.URL.Query().Get("timeout"))
	deadline := time.Now().Add(time.Duration(timeout) * time.Millisecond)
	for {
		s.mu.Lock()
		join := map[string]any{}
		pos := len(s.events)
		for _, e := range s.events[min(since, pos):] {
			rm := s.rooms[e.room]
			if rm.membership(user) != memberJoin || e.typ != "m.room.message" {
				continue
			}
			entry, _ := join[e.room].(map[string]any)
			if entry == nil {
				entry = map[string]any{"timeline": map[string]any{"events": []any{}, "limited": false}}
				join[e.room] = entry
			}
			tl := entry["timeline"].(map[string]any)
			tl["events"] = append(tl["events"].([]any), map[string]any{"type": e.typ, keyEventID: e.id,
				"sender": e.sender, "origin_server_ts": e.ts, "content": maps.Clone(e.content)})
		}
		if len(join) > 0 || !time.Now().Before(deadline) {
			s.calls["sync"]++
			s.mu.Unlock()
			writeJSON(w, http.StatusOK, map[string]any{"next_batch": "s" + strconv.Itoa(pos),
				"rooms": map[string]any{memberJoin: join}})
			return
		}
		s.mu.Unlock()
		select {
		case <-r.Context().Done():
			return
		case <-time.After(10 * time.Millisecond):
		}
	}
}
