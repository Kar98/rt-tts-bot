package twitchchat

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	twitch "github.com/gempir/go-twitch-irc/v4"
)

// ircClient is the subset of the go-twitch-irc client that LiveSource uses.
type ircClient interface {
	OnPrivateMessage(func(twitch.PrivateMessage))
	Join(channels ...string)
	Connect() error
	Disconnect() error
}

// LiveSource joins a channel anonymously and listens to chat until either
// MaxDuration passes or MaxMessages messages have arrived.
type LiveSource struct {
	// newClient overrides the IRC client constructor in tests.
	newClient func() ircClient
}

// shutdownGrace bounds how long Fetch waits for the IRC connection to close.
const shutdownGrace = 5 * time.Second

// Fetch implements Source.
func (s *LiveSource) Fetch(ctx context.Context, req Request) ([]Message, error) {
	channel := NormaliseChannel(req.Channel)
	if channel == "" {
		return nil, errors.New("twitchchat: channel is required")
	}
	if req.MaxMessages <= 0 {
		return nil, errors.New("twitchchat: MaxMessages must be positive")
	}
	if req.MaxDuration <= 0 {
		return nil, errors.New("twitchchat: MaxDuration must be positive")
	}

	newClient := s.newClient
	if newClient == nil {
		newClient = func() ircClient { return twitch.NewAnonymousClient() }
	}
	client := newClient()

	var (
		mu   sync.Mutex
		msgs []Message
		full = make(chan struct{})
		once sync.Once
	)
	client.OnPrivateMessage(func(m twitch.PrivateMessage) {
		mu.Lock()
		defer mu.Unlock()
		if len(msgs) >= req.MaxMessages {
			return
		}
		user := m.User.DisplayName
		if user == "" {
			user = m.User.Name
		}
		msgs = append(msgs, Message{User: user, Text: m.Message, Time: m.Time})
		if len(msgs) >= req.MaxMessages {
			once.Do(func() { close(full) })
		}
	})
	client.Join(channel)

	connErr := make(chan error, 1)
	go func() { connErr <- client.Connect() }()

	timer := time.NewTimer(req.MaxDuration)
	defer timer.Stop()

	var err error
	select {
	case <-timer.C:
	case <-full:
	case <-ctx.Done():
		err = ctx.Err()
	case cerr := <-connErr:
		connErr = nil
		if cerr != nil && !errors.Is(cerr, twitch.ErrClientDisconnected) {
			err = fmt.Errorf("twitchchat: connect to #%s: %w", channel, cerr)
		}
	}
	if connErr != nil {
		shutdown(client, connErr)
	}

	mu.Lock()
	defer mu.Unlock()
	if err != nil {
		return nil, err
	}
	out := make([]Message, len(msgs))
	copy(out, msgs)
	return out, nil
}

// shutdown disconnects the client and waits for Connect to return. Disconnect
// fails if the connection is not open yet, so it is retried until Connect
// returns or the grace period ends.
func shutdown(client ircClient, connErr <-chan error) {
	deadline := time.After(shutdownGrace)
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	for {
		_ = client.Disconnect()
		select {
		case <-connErr:
			return
		case <-deadline:
			return
		case <-tick.C:
		}
	}
}
