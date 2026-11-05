// Package matrix is the ChatHost adapter for Matrix (docs/plans/chat-matrix.md):
// the client-server API over net/http, with no SDK and no cgo. It speaks as
// one bot, by its access token, to one homeserver.
package matrix

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/JairJosafath/theos/internal/ports"
)

// apiPrefix is where the client-server API lives on a homeserver.
const apiPrefix = "/_matrix/client/v3"

// maxBody bounds a response read: a sync of many rooms is the biggest.
const maxBody = 16 << 20

// Defaults of a Client's rate-limit handling.
const (
	// defaultRetries is how many times a rate-limited call is made again.
	defaultRetries = 5
	// defaultMaxWait bounds one wait the homeserver asks for.
	defaultMaxWait = 30 * time.Second
	// defaultWait is the wait when a 429 names none.
	defaultWait = time.Second
)

// Matrix's event types and relations the adapter writes and reads.
const (
	typeMessage     = "m.room.message"
	typeReaction    = "m.reaction"
	typeRoomName    = "m.room.name"
	typeMember      = "m.room.member"
	typeSpaceChild  = "m.space.child"
	typeSpaceParent = "m.space.parent"
	relThread       = "m.thread"
	relReplace      = "m.replace"
	relAnnotation   = "m.annotation"
	formatHTML      = "org.matrix.custom.html"
	// msgNotice is a bot's message type: other bots don't answer it.
	msgNotice = "m.notice"
	// keyEventID names an event in a relation and in an answer; keyRelType
	// a relation's kind.
	keyEventID = "event_id"
	keyRelType = "rel_type"
)

// Client is a ChatHost on one homeserver, as one bot.
type Client struct {
	base    string
	token   string
	http    *http.Client
	retries int
	maxWait time.Duration
	// txnPrefix and txn make each send's transaction ID, the same for a
	// call's retries so the homeserver posts it once.
	txnPrefix string
	txn       atomic.Uint64

	mu sync.Mutex
	me string
}

var _ ports.ChatHost = (*Client)(nil)

// Option sets a Client's option.
type Option func(*Client)

// WithHTTPClient makes the Client's calls with c.
func WithHTTPClient(c *http.Client) Option { return func(m *Client) { m.http = c } }

// WithRetries sets how many times a rate-limited call is made again, and
// the longest wait it takes.
func WithRetries(n int, maxWait time.Duration) Option {
	return func(m *Client) { m.retries, m.maxWait = n, maxWait }
}

// New is a Client of cfg's homeserver, http(s)://host[:port] with an
// optional path, speaking with cfg's token.
func New(cfg ports.ChatConfig, opts ...Option) (*Client, error) {
	u, err := url.Parse(cfg.Homeserver)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.RawQuery != "" ||
		u.Fragment != "" {
		return nil, fmt.Errorf("homeserver %q is not an http or https address", cfg.Homeserver)
	}
	if cfg.Token == "" {
		return nil, fmt.Errorf("%w: it is empty", ports.ErrChatCredentialRefused)
	}
	c := &Client{base: strings.TrimSuffix(cfg.Homeserver, "/"), token: cfg.Token, http: http.DefaultClient,
		retries: defaultRetries, maxWait: defaultMaxWait, txnPrefix: rand.Text()[:12]}
	for _, opt := range opts {
		opt(c)
	}
	return c, nil
}

// Hosts builds the Client of each Chat's homeserver and bot with httpClient
// (nil for http.DefaultClient), for cmd/main.go.
func Hosts(httpClient *http.Client) ports.ChatHosts {
	return func(cfg ports.ChatConfig) (ports.ChatHost, error) {
		if httpClient == nil {
			return New(cfg)
		}
		return New(cfg, WithHTTPClient(httpClient))
	}
}

// Error is a homeserver's refusal: its HTTP status and Matrix's errcode.
type Error struct {
	Status  int
	Code    string
	Message string
}

func (e *Error) Error() string {
	return fmt.Sprintf("matrix: %d %s: %s", e.Status, e.Code, e.Message)
}

// Unwrap makes a refused token ports.ErrChatCredentialRefused.
func (e *Error) Unwrap() error {
	if e.Status == http.StatusUnauthorized || e.Code == "M_UNKNOWN_TOKEN" || e.Code == "M_MISSING_TOKEN" {
		return ports.ErrChatCredentialRefused
	}
	return nil
}

// errCode is the errcode of err, a homeserver's Error, or "".
func errCode(err error) string {
	if e, ok := errors.AsType[*Error](err); ok {
		return e.Code
	}
	return ""
}

// call makes one API call: body, when not nil, as JSON; the answer, when
// out is not nil, into out. A 429 is made again after the wait the
// homeserver asks for, up to the Client's retries.
func (c *Client) call(ctx context.Context, method, path string, query url.Values, body, out any) error {
	var data []byte
	if body != nil {
		var err error
		if data, err = json.Marshal(body); err != nil {
			return err
		}
	}
	target := c.base + apiPrefix + path
	if len(query) > 0 {
		target += "?" + query.Encode()
	}
	for attempt := 0; ; attempt++ {
		status, answer, header, err := c.once(ctx, method, target, data)
		if err != nil {
			return err
		}
		if status >= 200 && status < 300 {
			if out == nil {
				return nil
			}
			if err := json.Unmarshal(answer, out); err != nil {
				return fmt.Errorf("matrix: reading %s %s: %w", method, path, err)
			}
			return nil
		}
		var e struct {
			Code         string `json:"errcode"`
			Message      string `json:"error"`
			RetryAfterMS int64  `json:"retry_after_ms"`
		}
		_ = json.Unmarshal(answer, &e)
		if status != http.StatusTooManyRequests || attempt >= c.retries {
			return &Error{Status: status, Code: e.Code, Message: e.Message}
		}
		if err := c.wait(ctx, retryAfter(e.RetryAfterMS, header)); err != nil {
			return err
		}
	}
}

// retryAfter is the wait a 429 asks for: retry_after_ms, else the
// Retry-After header's seconds, else defaultWait.
func retryAfter(ms int64, header http.Header) time.Duration {
	if ms > 0 {
		return time.Duration(ms) * time.Millisecond
	}
	if s, err := strconv.Atoi(header.Get("Retry-After")); err == nil && s >= 0 {
		return time.Duration(s) * time.Second
	}
	return defaultWait
}

// wait sleeps d, at most the Client's maxWait, or until ctx ends.
func (c *Client) wait(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(min(d, c.maxWait))
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// once makes one HTTP request and reads its answer.
func (c *Client) once(ctx context.Context, method, target string, data []byte) (int, []byte, http.Header, error) {
	var body io.Reader
	if data != nil {
		body = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, target, body)
	if err != nil {
		return 0, nil, nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/json")
	if data != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return 0, nil, nil, fmt.Errorf("matrix: %s: %w", method, err)
	}
	defer func() { _ = resp.Body.Close() }()
	answer, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return 0, nil, nil, fmt.Errorf("matrix: reading the answer: %w", err)
	}
	return resp.StatusCode, answer, resp.Header, nil
}

// pathOf joins escaped segments into an API path.
func pathOf(segments ...string) string {
	var b strings.Builder
	for _, s := range segments {
		b.WriteString("/" + url.PathEscape(s))
	}
	return b.String()
}

// WhoAmI is the bot's user ID, asked once.
func (c *Client) WhoAmI(ctx context.Context) (string, error) {
	c.mu.Lock()
	me := c.me
	c.mu.Unlock()
	if me != "" {
		return me, nil
	}
	var answer struct {
		UserID string `json:"user_id"`
	}
	if err := c.call(ctx, http.MethodGet, "/account/whoami", nil, nil, &answer); err != nil {
		return "", err
	}
	if answer.UserID == "" {
		return "", errors.New("matrix: whoami named no user")
	}
	c.mu.Lock()
	c.me = answer.UserID
	c.mu.Unlock()
	return answer.UserID, nil
}

// serverName is the bot's homeserver's name, the part of its user ID after
// the first colon: where its aliases live.
func (c *Client) serverName(ctx context.Context) (string, error) {
	me, err := c.WhoAmI(ctx)
	if err != nil {
		return "", err
	}
	_, server, ok := strings.Cut(me, ":")
	if !ok || server == "" {
		return "", fmt.Errorf("matrix: user ID %q names no server", me)
	}
	return server, nil
}

// joinedRooms are the rooms the bot is in.
func (c *Client) joinedRooms(ctx context.Context) (map[string]bool, error) {
	var answer struct {
		Rooms []string `json:"joined_rooms"`
	}
	if err := c.call(ctx, http.MethodGet, "/joined_rooms", nil, nil, &answer); err != nil {
		return nil, err
	}
	joined := make(map[string]bool, len(answer.Rooms))
	for _, r := range answer.Rooms {
		joined[r] = true
	}
	return joined, nil
}

// resolveAlias is the room alias points to, or "" when it points nowhere.
func (c *Client) resolveAlias(ctx context.Context, alias string) (string, error) {
	var answer struct {
		RoomID string `json:"room_id"`
	}
	err := c.call(ctx, http.MethodGet, pathOf("directory", "room", alias), nil, nil, &answer)
	if errCode(err) == "M_NOT_FOUND" {
		return "", nil
	}
	return answer.RoomID, err
}

// state reads the content of room's state event typ at key into out, and
// reports false when there is none.
func (c *Client) state(ctx context.Context, room, typ, key string, out any) (bool, error) {
	err := c.call(ctx, http.MethodGet, pathOf("rooms", room, "state", typ, key), nil, nil, out)
	if errCode(err) == "M_NOT_FOUND" {
		return false, nil
	}
	return err == nil, err
}

// setState writes room's state event typ at key.
func (c *Client) setState(ctx context.Context, room, typ, key string, content any) error {
	return c.call(ctx, http.MethodPut, pathOf("rooms", room, "state", typ, key), nil, content, nil)
}

// via is the content of a Space link: the server the room is reached by.
func via(server string) map[string]any { return map[string]any{"via": []string{server}} }

// EnsureRoom finds spec's room by its ID, then its alias, among the bot's
// rooms, or makes it. A room found keeps its alias only when the alias
// still points to it.
func (c *Client) EnsureRoom(ctx context.Context, spec ports.ChatRoomSpec) (ports.ChatRoom, error) {
	server, err := c.serverName(ctx)
	if err != nil {
		return ports.ChatRoom{}, err
	}
	joined, err := c.joinedRooms(ctx)
	if err != nil {
		return ports.ChatRoom{}, err
	}
	alias := ""
	if spec.Alias != "" {
		alias = "#" + spec.Alias + ":" + server
	}
	room := ports.ChatRoom{}
	if spec.ID != "" && joined[spec.ID] {
		room.ID = spec.ID
	}
	if alias != "" {
		found, err := c.resolveAlias(ctx, alias)
		switch {
		case err != nil:
			return ports.ChatRoom{}, err
		case found != "" && room.ID == "" && !joined[found]:
			return ports.ChatRoom{}, &ports.ChatAliasTakenError{Alias: alias}
		case found != "" && room.ID == "":
			room.ID, room.Alias = found, alias
		case found != "" && found == room.ID:
			room.Alias = alias
		}
	}
	if room.ID == "" {
		return c.createRoom(ctx, spec, alias, server)
	}
	if err := c.keepName(ctx, room.ID, spec.Name); err != nil {
		return ports.ChatRoom{}, err
	}
	if spec.Parent != "" {
		if err := c.keepLinks(ctx, spec.Parent, room.ID, server); err != nil {
			return ports.ChatRoom{}, err
		}
	}
	return room, nil
}

// createRoom makes spec's room, private, invite only and unencrypted, with
// alias, and links it into its Space.
func (c *Client) createRoom(ctx context.Context, spec ports.ChatRoomSpec, alias, server string) (ports.ChatRoom, error) {
	body := map[string]any{"preset": "private_chat", "visibility": "private"}
	if spec.Name != "" {
		body["name"] = spec.Name
	}
	if spec.Topic != "" {
		body["topic"] = spec.Topic
	}
	if spec.Alias != "" {
		body["room_alias_name"] = spec.Alias
	}
	if spec.Space {
		body["creation_content"] = map[string]any{"type": "m.space"}
	}
	if spec.Parent != "" {
		parent := via(server)
		parent["canonical"] = true
		body["initial_state"] = []map[string]any{{"type": typeSpaceParent, "state_key": spec.Parent, "content": parent}}
	}
	var answer struct {
		RoomID string `json:"room_id"`
	}
	err := c.call(ctx, http.MethodPost, "/createRoom", nil, body, &answer)
	if errCode(err) == "M_ROOM_IN_USE" {
		return ports.ChatRoom{}, &ports.ChatAliasTakenError{Alias: alias}
	}
	if err != nil {
		return ports.ChatRoom{}, err
	}
	if spec.Parent != "" {
		if err := c.setState(ctx, spec.Parent, typeSpaceChild, answer.RoomID, via(server)); err != nil {
			return ports.ChatRoom{}, err
		}
	}
	return ports.ChatRoom{ID: answer.RoomID, Alias: alias, Created: true}, nil
}

// keepName sets room's name to name when it differs.
func (c *Client) keepName(ctx context.Context, room, name string) error {
	if name == "" {
		return nil
	}
	var current struct {
		Name string `json:"name"`
	}
	if _, err := c.state(ctx, room, typeRoomName, "", &current); err != nil {
		return err
	}
	if current.Name == name {
		return nil
	}
	return c.setState(ctx, room, typeRoomName, "", map[string]string{"name": name})
}

// keepLinks makes space list room as a child and room name space its
// parent, when either link is missing.
func (c *Client) keepLinks(ctx context.Context, space, room, server string) error {
	var link struct {
		Via []string `json:"via"`
	}
	ok, err := c.state(ctx, space, typeSpaceChild, room, &link)
	if err != nil {
		return err
	}
	if !ok || len(link.Via) == 0 {
		if err := c.setState(ctx, space, typeSpaceChild, room, via(server)); err != nil {
			return err
		}
	}
	link.Via = nil
	if ok, err = c.state(ctx, room, typeSpaceParent, space, &link); err != nil {
		return err
	}
	if ok && len(link.Via) > 0 {
		return nil
	}
	parent := via(server)
	parent["canonical"] = true
	return c.setState(ctx, room, typeSpaceParent, space, parent)
}

// Invite invites user to room. A homeserver refuses to invite a user
// already invited or in the room; that is no error.
func (c *Client) Invite(ctx context.Context, room, user string) error {
	err := c.call(ctx, http.MethodPost, pathOf("rooms", room, "invite"), nil, map[string]string{"user_id": user}, nil)
	if e, ok := errors.AsType[*Error](err); !ok || e.Status != http.StatusForbidden {
		return err
	}
	var member struct {
		Membership string `json:"membership"`
	}
	if ok, merr := c.state(ctx, room, typeMember, user, &member); merr == nil && ok &&
		(member.Membership == "invite" || member.Membership == "join") {
		return nil
	}
	return err
}

// content is msg as a message's content: a notice, with its HTML when it
// has one.
func content(msg ports.ChatMessage) map[string]any {
	c := map[string]any{"msgtype": msgNotice, "body": msg.Text}
	if msg.HTML != "" {
		c["format"] = formatHTML
		c["formatted_body"] = msg.HTML
	}
	return c
}

// send posts an event of typ with content in room.
func (c *Client) send(ctx context.Context, room, typ string, content any) (string, error) {
	txn := fmt.Sprintf("theos.%s.%d", c.txnPrefix, c.txn.Add(1))
	var answer struct {
		EventID string `json:"event_id"`
	}
	if err := c.call(ctx, http.MethodPut, pathOf("rooms", room, "send", typ, txn), nil, content, &answer); err != nil {
		return "", err
	}
	return answer.EventID, nil
}

// Send posts msg in room.
func (c *Client) Send(ctx context.Context, room string, msg ports.ChatMessage) (string, error) {
	return c.send(ctx, room, typeMessage, content(msg))
}

// Reply posts msg in the thread whose root is threadRoot; a client without
// threads shows it as a reply to the root.
func (c *Client) Reply(ctx context.Context, room, threadRoot string, msg ports.ChatMessage) (string, error) {
	body := content(msg)
	body["m.relates_to"] = map[string]any{keyRelType: relThread, keyEventID: threadRoot, "is_falling_back": true,
		"m.in_reply_to": map[string]string{keyEventID: threadRoot}}
	return c.send(ctx, room, typeMessage, body)
}

// Edit replaces event's text with msg; a client without edits shows the
// new text marked with a star.
func (c *Client) Edit(ctx context.Context, room, event string, msg ports.ChatMessage) (string, error) {
	body := content(ports.ChatMessage{Text: "* " + msg.Text})
	if msg.HTML != "" {
		body["format"] = formatHTML
		body["formatted_body"] = "* " + msg.HTML
	}
	body["m.new_content"] = content(msg)
	body["m.relates_to"] = map[string]string{keyRelType: relReplace, keyEventID: event}
	return c.send(ctx, room, typeMessage, body)
}

// React puts key on event.
func (c *Client) React(ctx context.Context, room, event, key string) (string, error) {
	return c.send(ctx, room, typeReaction, map[string]any{
		"m.relates_to": map[string]string{keyRelType: relAnnotation, keyEventID: event, "key": key}})
}

// syncFilter keeps a sync to the rooms' messages: no presence, account data,
// state or receipts.
const syncFilter = `{"presence":{"types":[]},"account_data":{"types":[]},` +
	`"room":{"state":{"types":[]},"ephemeral":{"types":[]},"account_data":{"types":[]},` +
	`"timeline":{"types":["m.room.message"]}}}`

// syncAnswer is the part of a /sync answer the adapter reads.
type syncAnswer struct {
	NextBatch string `json:"next_batch"`
	Rooms     struct {
		Join map[string]struct {
			Timeline struct {
				Events []syncEvent `json:"events"`
			} `json:"timeline"`
		} `json:"join"`
	} `json:"rooms"`
}

type syncEvent struct {
	Type    string `json:"type"`
	ID      string `json:"event_id"`
	Sender  string `json:"sender"`
	TS      int64  `json:"origin_server_ts"`
	Content struct {
		Body       string `json:"body"`
		NewContent *struct {
			Body string `json:"body"`
		} `json:"m.new_content"`
		RelatesTo *struct {
			RelType string `json:"rel_type"`
			EventID string `json:"event_id"`
		} `json:"m.relates_to"`
	} `json:"content"`
}

// Sync returns the people's messages since since, the bot's own left out,
// oldest first.
func (c *Client) Sync(ctx context.Context, since string, wait time.Duration) (ports.ChatSync, error) {
	me, err := c.WhoAmI(ctx)
	if err != nil {
		return ports.ChatSync{}, err
	}
	query := url.Values{"timeout": {strconv.FormatInt(wait.Milliseconds(), 10)}, "filter": {syncFilter}}
	if since != "" {
		query.Set("since", since)
	}
	var answer syncAnswer
	if err := c.call(ctx, http.MethodGet, "/sync", query, nil, &answer); err != nil {
		return ports.ChatSync{}, err
	}
	out := ports.ChatSync{Next: answer.NextBatch}
	for room, joined := range answer.Rooms.Join {
		for _, e := range joined.Timeline.Events {
			if e.Type != typeMessage || e.Sender == me {
				continue
			}
			ev := ports.ChatEvent{Room: room, ID: e.ID, Sender: e.Sender, Text: e.Content.Body,
				Time: time.UnixMilli(e.TS).UTC()}
			if rel := e.Content.RelatesTo; rel != nil {
				switch rel.RelType {
				case relThread:
					ev.ThreadRoot = rel.EventID
				case relReplace:
					ev.Replaces = rel.EventID
					if e.Content.NewContent != nil {
						ev.Text = e.Content.NewContent.Body
					}
				}
			}
			out.Events = append(out.Events, ev)
		}
	}
	slices.SortStableFunc(out.Events, func(a, b ports.ChatEvent) int {
		if c := a.Time.Compare(b.Time); c != 0 {
			return c
		}
		return strings.Compare(a.Room, b.Room)
	})
	return out, nil
}
