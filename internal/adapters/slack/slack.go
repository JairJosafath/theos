/*
Copyright 2026 The theos Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

// Package slack is the ChatHost adapter for Slack
// (docs/plans/requests-and-chat.md, decisions 4 to 9): the Web API over
// net/http and Socket Mode over golang.org/x/net/websocket, with no SDK. It
// speaks as one app's bot, by its bot token, to one workspace; the app-level
// token opens the socket, on the first Sync and by nothing else.
//
// A room is a private channel named by its alias, and there is no Space. A
// thread is a Slack thread and a message its ts, unique in its channel. A
// post goes out in mrkdwn (model.ChatPost.Mrkdwn), and what people type is
// decoded (Decode) before it leaves the port.
package slack

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/JairJosafath/theos/internal/ports"
)

// DefaultAddress is Slack's Web API.
const DefaultAddress = "https://slack.com/api"

// maxBody bounds a response read: a page of 200 messages is the biggest.
const maxBody = 16 << 20

// Defaults of a Client's rate-limit handling.
const (
	// defaultRetries is how many times a rate-limited call is made again.
	defaultRetries = 5
	// defaultMaxWait bounds one wait Slack asks for.
	defaultMaxWait = 30 * time.Second
	// defaultWait is the wait when a 429 names none.
	defaultWait = time.Second
)

// pageSize is how many items a listing asks for at once, as Slack
// recommends at most.
const pageSize = 200

// The limits of a section block's text and of a message's blocks.
const (
	maxSectionText = 3000
	maxBlocks      = 50
)

// The Web API's arguments the adapter gives more than once.
const (
	argChannel = "channel"
	argLimit   = "limit"
	argText    = "text"
)

// The error codes the adapter reads.
const (
	codeMissingScope     = "missing_scope"
	codeNameTaken        = "name_taken"
	codeAlreadyInChannel = "already_in_channel"
	codeCantInviteSelf   = "cant_invite_self"
	codeAlreadyReacted   = "already_reacted"
)

// refusedCodes are the error codes of a token Slack doesn't take.
var refusedCodes = map[string]bool{"invalid_auth": true, "not_authed": true, "account_inactive": true,
	"token_revoked": true, "token_expired": true, "not_allowed_token_type": true, "missing_args": true}

// Client is a ChatHost on one Slack workspace, as one app's bot.
type Client struct {
	base     string
	token    string
	appToken string
	http     *http.Client
	retries  int
	maxWait  time.Duration

	mu    sync.Mutex
	me    string // the bot's user ID
	botID string // the bot's bot ID, which its messages carry

	feed *feed
}

var _ ports.ChatHost = (*Client)(nil)

// Option sets a Client's option.
type Option func(*Client)

// WithHTTPClient makes the Client's Web API calls with c. The socket is
// dialled directly: golang.org/x/net/websocket reads no proxy setting
// (decision 7).
func WithHTTPClient(c *http.Client) Option { return func(s *Client) { s.http = c } }

// WithRetries sets how many times a rate-limited call is made again, and
// the longest wait it takes.
func WithRetries(n int, maxWait time.Duration) Option {
	return func(s *Client) { s.retries, s.maxWait = n, maxWait }
}

// New is a Client of cfg: Address is the Web API's address (DefaultAddress,
// or GovSlack's), Token the bot token and AppToken the app-level token,
// which only Sync needs.
func New(cfg ports.ChatConfig, opts ...Option) (*Client, error) {
	u, err := url.Parse(cfg.Address)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.RawQuery != "" ||
		u.Fragment != "" {
		return nil, fmt.Errorf("slack: address %q is not an http or https address", cfg.Address)
	}
	if cfg.Token == "" {
		return nil, fmt.Errorf("%w: it is empty", ports.ErrChatCredentialRefused)
	}
	c := &Client{base: strings.TrimSuffix(cfg.Address, "/"), token: cfg.Token, appToken: cfg.AppToken,
		http: http.DefaultClient, retries: defaultRetries, maxWait: defaultMaxWait}
	for _, opt := range opts {
		opt(c)
	}
	c.feed = newFeed()
	return c, nil
}

// Hosts builds the Client of each Chat's workspace and bot with httpClient
// (nil for http.DefaultClient). Nothing calls it yet: step 8 of
// docs/plans/requests-and-chat.md wires it into cmd/.
func Hosts(httpClient *http.Client) ports.ChatHosts {
	return func(cfg ports.ChatConfig) (ports.ChatHost, error) {
		if httpClient == nil {
			return New(cfg)
		}
		return New(cfg, WithHTTPClient(httpClient))
	}
}

// Close closes the socket, if one is open, and stops dialling again.
func (c *Client) Close() { c.feed.close() }

// Error is Slack's refusal of a Web API call: the HTTP status, Slack's
// error code and, for missing_scope, the scope the call needs.
type Error struct {
	Method string
	Status int
	Code   string
	Needed string
}

func (e *Error) Error() string {
	msg := fmt.Sprintf("slack: %s: %d %s", e.Method, e.Status, e.Code)
	if e.Needed != "" {
		msg += " (needs " + e.Needed + ")"
	}
	return msg
}

// Unwrap makes a refused token ports.ErrChatCredentialRefused and a missing
// scope a ports.ChatScopeMissingError naming it.
func (e *Error) Unwrap() error {
	switch {
	case refusedCodes[e.Code] || e.Status == http.StatusUnauthorized:
		return ports.ErrChatCredentialRefused
	case e.Code == codeMissingScope:
		return &ports.ChatScopeMissingError{Scope: e.Needed}
	}
	return nil
}

// errCode is the code of err, Slack's Error, or "".
func errCode(err error) string {
	if e, ok := errors.AsType[*Error](err); ok {
		return e.Code
	}
	return ""
}

// call makes one Web API call with token, its arguments as a form, and
// reads the answer into out when out is not nil. A 429 is made again after
// the wait Slack asks for, up to the Client's retries.
func (c *Client) call(ctx context.Context, token, method string, args url.Values, out any) error {
	body := args.Encode()
	for attempt := 0; ; attempt++ {
		status, answer, header, err := c.once(ctx, token, method, body)
		if err != nil {
			return err
		}
		var e struct {
			OK     bool   `json:"ok"`
			Code   string `json:"error"`
			Needed string `json:"needed"`
		}
		jsonErr := json.Unmarshal(answer, &e)
		if status == http.StatusTooManyRequests && attempt < c.retries {
			if err := c.wait(ctx, retryAfter(header)); err != nil {
				return err
			}
			continue
		}
		switch {
		case status != http.StatusOK:
			if e.Code == "" {
				e.Code = strings.ToLower(strings.ReplaceAll(http.StatusText(status), " ", "_"))
			}
			return &Error{Method: method, Status: status, Code: e.Code, Needed: e.Needed}
		case jsonErr != nil:
			return fmt.Errorf("slack: reading %s: %w", method, jsonErr)
		case !e.OK:
			return &Error{Method: method, Status: status, Code: e.Code, Needed: e.Needed}
		case out == nil:
			return nil
		}
		if err := json.Unmarshal(answer, out); err != nil {
			return fmt.Errorf("slack: reading %s: %w", method, err)
		}
		return nil
	}
}

// retryAfter is the wait a 429 asks for: the Retry-After header's seconds,
// else defaultWait.
func retryAfter(header http.Header) time.Duration {
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

// once makes one POST of a form and reads its answer. The token goes in the
// Authorization header, never in the form: apps.connections.open refuses
// it there.
func (c *Client) once(ctx context.Context, token, method, form string) (int, []byte, http.Header, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+"/"+method, strings.NewReader(form))
	if err != nil {
		return 0, nil, nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return 0, nil, nil, fmt.Errorf("slack: %s: %w", method, err)
	}
	defer func() { _ = resp.Body.Close() }()
	answer, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return 0, nil, nil, fmt.Errorf("slack: reading %s: %w", method, err)
	}
	return resp.StatusCode, answer, resp.Header, nil
}

// bot calls method as the bot.
func (c *Client) bot(ctx context.Context, method string, args url.Values, out any) error {
	return c.call(ctx, c.token, method, args, out)
}

// WhoAmI is the bot's user ID, asked once with auth.test.
// It learns the bot's bot ID with it, which the bot's messages carry.
func (c *Client) WhoAmI(ctx context.Context) (string, error) {
	c.mu.Lock()
	me := c.me
	c.mu.Unlock()
	if me != "" {
		return me, nil
	}
	var answer struct {
		UserID string `json:"user_id"`
		BotID  string `json:"bot_id"`
	}
	if err := c.bot(ctx, "auth.test", nil, &answer); err != nil {
		return "", err
	}
	if answer.UserID == "" {
		return "", errors.New("slack: auth.test named no user")
	}
	c.mu.Lock()
	c.me, c.botID = answer.UserID, answer.BotID
	c.mu.Unlock()
	return answer.UserID, nil
}

// channel is a conversation as users.conversations and conversations.create
// give it.
type channel struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// page is the cursor of a listing's next page.
type page struct {
	Meta struct {
		NextCursor string `json:"next_cursor"`
	} `json:"response_metadata"`
}

// channels are the private channels the bot is in, archived ones left out.
func (c *Client) channels(ctx context.Context) ([]channel, error) {
	var all []channel
	cursor := ""
	for {
		args := url.Values{"types": {"private_channel"}, "exclude_archived": {"true"},
			argLimit: {strconv.Itoa(pageSize)}}
		if cursor != "" {
			args.Set("cursor", cursor)
		}
		var answer struct {
			page
			Channels []channel `json:"channels"`
		}
		if err := c.bot(ctx, "users.conversations", args, &answer); err != nil {
			return nil, err
		}
		all = append(all, answer.Channels...)
		if cursor = answer.Meta.NextCursor; cursor == "" {
			return all, nil
		}
	}
}

// ErrNoSpaces is a Space, or a room in one, asked of Slack: the workspace
// is the Space (decision 5).
var ErrNoSpaces = errors.New("slack: a workspace has no Spaces; its channels stand alone")

// alias is a channel's name as Slack spells it to people.
func alias(name string) string { return "#" + name }

// EnsureRoom finds spec's private channel by its ID, then by its name, the
// alias, among the channels the bot is in, or makes it with the spec's
// topic. A channel's one name is its alias, so a name in spec is not
// applied (decision 4). A name Slack says is taken, by a channel the bot is
// not in or was removed from, is a ChatAliasTakenError.
func (c *Client) EnsureRoom(ctx context.Context, spec ports.ChatRoomSpec) (ports.ChatRoom, error) {
	if spec.Space || spec.Parent != "" {
		return ports.ChatRoom{}, ErrNoSpaces
	}
	if _, err := c.WhoAmI(ctx); err != nil {
		return ports.ChatRoom{}, err
	}
	in, err := c.channels(ctx)
	if err != nil {
		return ports.ChatRoom{}, err
	}
	for _, ch := range in {
		if spec.ID != "" && ch.ID == spec.ID {
			return ports.ChatRoom{ID: ch.ID, Alias: alias(ch.Name)}, nil
		}
	}
	for _, ch := range in {
		if spec.Alias != "" && ch.Name == spec.Alias {
			return ports.ChatRoom{ID: ch.ID, Alias: alias(ch.Name)}, nil
		}
	}
	if spec.Alias == "" {
		return ports.ChatRoom{}, fmt.Errorf("slack: the bot is in no channel %s, and no name was given to make one",
			spec.ID)
	}
	var made struct {
		Channel channel `json:"channel"`
	}
	err = c.bot(ctx, "conversations.create", url.Values{"name": {spec.Alias}, "is_private": {"true"}}, &made)
	if errCode(err) == codeNameTaken {
		return ports.ChatRoom{}, &ports.ChatAliasTakenError{Alias: alias(spec.Alias)}
	}
	if err != nil {
		return ports.ChatRoom{}, err
	}
	if spec.Topic != "" {
		if err := c.bot(ctx, "conversations.setTopic", url.Values{argChannel: {made.Channel.ID},
			"topic": {spec.Topic}}, nil); err != nil {
			return ports.ChatRoom{}, err
		}
	}
	return ports.ChatRoom{ID: made.Channel.ID, Alias: alias(made.Channel.Name), Created: true}, nil
}

// Invite adds user to room. Slack has no pending invite: the person is in
// the channel at once, and someone already in it is no error.
func (c *Client) Invite(ctx context.Context, room, user string) error {
	err := c.bot(ctx, "conversations.invite", url.Values{argChannel: {room}, "users": {user}}, nil)
	if code := errCode(err); code == codeAlreadyInChannel || code == codeCantInviteSelf {
		return nil
	}
	return err
}

// escaper escapes the three characters Slack reads as control in a
// message's text.
var escaper = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")

// message is msg as chat.postMessage and chat.update take it: the plain
// text, escaped, as the text people's notifications and Slack's search
// show, and the formatted text, which is mrkdwn on Slack's row
// (model.ChatPost.Mrkdwn), as section blocks, which the channel shows.
func message(msg ports.ChatMessage) url.Values {
	args := url.Values{argText: {escaper.Replace(msg.Text)}}
	sections := sectionTexts(msg.Formatted)
	switch {
	case msg.Formatted == "":
		args.Set("blocks", "[]")
	case len(sections) > maxBlocks:
		// Too long for blocks: the mrkdwn as the text, which Slack cuts.
		args.Set(argText, msg.Formatted)
		args.Set("blocks", "[]")
	default:
		blocks := make([]map[string]any, 0, len(sections))
		for _, s := range sections {
			blocks = append(blocks, map[string]any{"type": "section",
				"text": map[string]string{"type": "mrkdwn", "text": s}})
		}
		b, _ := json.Marshal(blocks)
		args.Set("blocks", string(b))
	}
	return args
}

// sectionTexts cuts text into pieces a section block takes, at line breaks
// where it can.
func sectionTexts(text string) []string {
	var out []string
	var cur strings.Builder
	flush := func() {
		if cur.Len() > 0 {
			out = append(out, cur.String())
			cur.Reset()
		}
	}
	for line := range strings.SplitSeq(text, "\n") {
		for len([]rune(line)) > maxSectionText {
			flush()
			r := []rune(line)
			out = append(out, string(r[:maxSectionText]))
			line = string(r[maxSectionText:])
		}
		if cur.Len() > 0 && len([]rune(cur.String()))+1+len([]rune(line)) > maxSectionText {
			flush()
		}
		if cur.Len() > 0 {
			cur.WriteString("\n")
		}
		cur.WriteString(line)
	}
	flush()
	return out
}

// post posts args in room and returns the message's ts.
func (c *Client) post(ctx context.Context, room string, args url.Values) (string, error) {
	args.Set(argChannel, room)
	var answer struct {
		TS string `json:"ts"`
	}
	if err := c.bot(ctx, "chat.postMessage", args, &answer); err != nil {
		return "", err
	}
	return answer.TS, nil
}

// Send posts msg in room and returns its ts.
func (c *Client) Send(ctx context.Context, room string, msg ports.ChatMessage) (string, error) {
	return c.post(ctx, room, message(msg))
}

// Reply posts msg in the thread whose root is threadRoot.
func (c *Client) Reply(ctx context.Context, room, threadRoot string, msg ports.ChatMessage) (string, error) {
	args := message(msg)
	args.Set("thread_ts", threadRoot)
	return c.post(ctx, room, args)
}

// Edit replaces the bot's message event with msg, and returns its ts: an
// edit has no ID of its own on Slack.
func (c *Client) Edit(ctx context.Context, room, event string, msg ports.ChatMessage) (string, error) {
	args := message(msg)
	args.Set(argChannel, room)
	args.Set("ts", event)
	var answer struct {
		TS string `json:"ts"`
	}
	if err := c.bot(ctx, "chat.update", args, &answer); err != nil {
		return "", err
	}
	return answer.TS, nil
}

// emojiNames are Slack's names of the reaction keys the factory puts on a
// message (model.ChatDoneReaction is ✅).
var emojiNames = map[string]string{
	"✅":  "white_check_mark",
	"❌":  "x",
	"👀":  "eyes",
	"👍":  "+1",
	"👎":  "-1",
	"🎉":  "tada",
	"🚀":  "rocket",
	"⏳":  "hourglass_flowing_sand",
	"⚠️": "warning",
}

// React puts key on event with reactions.add, by the emoji's name. A key
// without a name here is an error, never a guess. A reaction has no ID of
// its own on Slack: the message's ts is returned.
func (c *Client) React(ctx context.Context, room, event, key string) (string, error) {
	name, ok := emojiNames[key]
	if !ok {
		return "", fmt.Errorf("slack: no emoji name for the reaction %q", key)
	}
	err := c.bot(ctx, "reactions.add", url.Values{argChannel: {room}, "name": {name}, "timestamp": {event}}, nil)
	if err != nil && errCode(err) != codeAlreadyReacted {
		return "", err
	}
	return event, nil
}
