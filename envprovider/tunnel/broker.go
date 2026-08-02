package tunnel

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync"
	"time"
)

// Broker is the platform side of the tunnel. Agents dial *out* to it and hand
// over connections; the platform then issues requests down those connections.
//
// This is what makes the transport a tunnel rather than an address rewrite: at
// no point does the platform connect to the agent. A user's machine needs no
// inbound firewall hole, no public address, and no port forward, which is the
// entire reason the tunnel provider exists.
type Broker struct {
	// Authenticate decides whether a dialing agent may register, and under
	// which instance name. Returning an error refuses the connection before
	// it can carry any traffic.
	Authenticate func(token string) (agent string, err error)

	mu     sync.Mutex
	agents map[string]*agentPool
}

// registration is the first frame an agent sends after dialing out. It is a
// single JSON line so the connection can be handed to an HTTP server
// afterwards without buffering surprises.
type registration struct {
	Token string `json:"token"`
	Agent string `json:"agent,omitempty"`
}

// agentPool holds the connections one agent has offered.
type agentPool struct {
	mu     sync.Mutex
	conns  []net.Conn
	closed bool
	// issued are connections handed to a transport. They are tracked so a
	// disconnect really disconnects rather than leaving live ones in the
	// caller's idle pool.
	issued []net.Conn
	// closers drop a transport's idle connections, which it owns rather than
	// this pool.
	closers []func()
	// waiters are dial attempts parked until a connection is offered, so a
	// request that arrives just before the agent reconnects is served rather
	// than failed.
	waiters []chan net.Conn
}

// NewBroker builds a broker.
func NewBroker(authenticate func(token string) (string, error)) *Broker {
	return &Broker{Authenticate: authenticate, agents: map[string]*agentPool{}}
}

// Serve accepts agent connections on a listener until the context is done.
// The listener is the platform's own address, which agents reach outbound.
func (b *Broker) Serve(ctx context.Context, listener net.Listener) error {
	go func() {
		<-ctx.Done()
		_ = listener.Close()
	}()
	for {
		conn, err := listener.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		go b.accept(conn)
	}
}

// accept authenticates one offered connection and files it under its agent.
func (b *Broker) accept(conn net.Conn) {
	// A dialing agent that never identifies itself must not hold a slot.
	_ = conn.SetReadDeadline(time.Now().Add(30 * time.Second))
	reader := bufio.NewReader(conn)
	line, err := reader.ReadBytes('\n')
	if err != nil {
		_ = conn.Close()
		return
	}
	var reg registration
	if err := json.Unmarshal(line, &reg); err != nil {
		_ = conn.Close()
		return
	}
	agent, err := b.Authenticate(reg.Token)
	if err != nil || agent == "" {
		_ = conn.Close()
		return
	}
	if _, err := conn.Write([]byte("ok\n")); err != nil {
		_ = conn.Close()
		return
	}
	_ = conn.SetReadDeadline(time.Time{})
	// Any bytes the agent already buffered belong to the HTTP stream, so the
	// reader is carried forward rather than discarded.
	b.pool(agent).offer(&bufferedConn{Conn: conn, reader: reader})
}

// bufferedConn preserves bytes read during registration.
type bufferedConn struct {
	net.Conn
	reader *bufio.Reader
}

func (c *bufferedConn) Read(p []byte) (int, error) { return c.reader.Read(p) }

func (b *Broker) pool(agent string) *agentPool {
	b.mu.Lock()
	defer b.mu.Unlock()
	existing, ok := b.agents[agent]
	if !ok {
		existing = &agentPool{}
		b.agents[agent] = existing
	}
	return existing
}

// Connected reports whether an agent currently has any usable connection.
func (b *Broker) Connected(agent string) bool {
	b.mu.Lock()
	pool, ok := b.agents[agent]
	b.mu.Unlock()
	if !ok {
		return false
	}
	pool.mu.Lock()
	defer pool.mu.Unlock()
	return len(pool.conns) > 0
}

// Disconnect drops every connection an agent has offered, which is what a
// severed tunnel looks like from the platform's side.
//
// Connections already handed to a transport are closed too, via the closer the
// transport registered. Without that, the platform's own idle pool would keep
// serving requests to an agent that is gone, and a lost laptop would keep
// looking healthy.
func (b *Broker) Disconnect(agent string) {
	b.mu.Lock()
	pool, ok := b.agents[agent]
	b.mu.Unlock()
	if !ok {
		return
	}
	pool.mu.Lock()
	conns := pool.conns
	pool.conns = nil
	issued := pool.issued
	pool.issued = nil
	closers := pool.closers
	pool.mu.Unlock()
	for _, conn := range append(conns, issued...) {
		_ = conn.Close()
	}
	for _, close := range closers {
		close()
	}
}

func (p *agentPool) offer(conn net.Conn) {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		_ = conn.Close()
		return
	}
	// A parked request takes the connection directly.
	for len(p.waiters) > 0 {
		waiter := p.waiters[0]
		p.waiters = p.waiters[1:]
		select {
		case waiter <- conn:
			p.mu.Unlock()
			return
		default:
		}
	}
	p.conns = append(p.conns, conn)
	p.mu.Unlock()
}

// take removes one connection, waiting briefly for a reconnect rather than
// failing a request that raced one.
func (p *agentPool) take(ctx context.Context, wait time.Duration) (net.Conn, error) {
	p.mu.Lock()
	if len(p.conns) > 0 {
		conn := p.conns[0]
		p.conns = p.conns[1:]
		p.issued = append(p.issued, conn)
		p.mu.Unlock()
		return conn, nil
	}
	waiter := make(chan net.Conn, 1)
	p.waiters = append(p.waiters, waiter)
	p.mu.Unlock()

	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case conn := <-waiter:
		p.mu.Lock()
		p.issued = append(p.issued, conn)
		p.mu.Unlock()
		return conn, nil
	case <-timer.C:
		return nil, ErrUnreachable
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// DefaultOfferWait is how long a request waits for an agent connection.
//
// It trades two failures against each other. Too short, and a burst that
// briefly outpaces the agent's redial looks like a lost laptop, suspending a
// healthy environment. Too long, and a genuinely gone agent is not noticed for
// as long as the wait, delaying the suspension that protects the workspace.
const DefaultOfferWait = 3 * time.Second

// Transport returns an HTTP transport whose requests travel down an agent's
// outbound connections. It is the only way the platform can reach an agent,
// which is what the anti-alias rule requires.
func (b *Broker) Transport(agent string) *http.Transport {
	return b.TransportWithWait(agent, DefaultOfferWait)
}

// TransportWithWait is Transport with an explicit wait for a connection.
func (b *Broker) TransportWithWait(agent string, wait time.Duration) *http.Transport {
	pool := b.pool(agent)
	transport := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			conn, err := pool.take(ctx, wait)
			if err != nil {
				return nil, fmt.Errorf("%w: agent %q has offered no connection", ErrUnreachable, agent)
			}
			return conn, nil
		},
		// Connections are reused. The agent serves each one until it ends, so
		// a keep-alive request travels down a connection that is already
		// established rather than waiting for the pool to be replenished.
		MaxConnsPerHost:     16,
		IdleConnTimeout:     30 * time.Second,
		MaxIdleConnsPerHost: 16,
	}
	pool.mu.Lock()
	pool.closers = append(pool.closers, transport.CloseIdleConnections)
	pool.mu.Unlock()
	return transport
}

// AgentDialer is the agent side: it dials out to the broker and serves HTTP on
// the connections it offers. The agent opens no listening socket at all, so
// there is no address for the platform to connect to even in principle.
type AgentDialer struct {
	// BrokerAddress is where the platform accepts agent connections.
	BrokerAddress string
	// Token identifies the agent to the broker.
	Token string
	// Handler serves the control API over each offered connection.
	Handler http.Handler
	// Connections is how many outbound connections to keep offered, which
	// bounds how many platform requests can be in flight at once.
	Connections int
}

// Run keeps the agent's outbound connections offered until the context ends.
// Losing the broker is expected, not fatal: a laptop that sleeps reconnects.
func (d *AgentDialer) Run(ctx context.Context) error {
	if d.Connections <= 0 {
		// Enough concurrency that a burst of platform requests does not
		// serialize behind one connection while others are being redialed.
		d.Connections = 16
	}
	if d.Handler == nil {
		return errors.New("tunnel: the agent dialer needs a handler")
	}
	var wg sync.WaitGroup
	for range d.Connections {
		wg.Add(1)
		go func() {
			defer wg.Done()
			d.maintain(ctx)
		}()
	}
	wg.Wait()
	return ctx.Err()
}

// maintain keeps one outbound connection offered, redialing after each use.
//
// Redialing is deliberately paced. An unpaced loop that reconnects the instant
// a connection ends will churn thousands of sockets a second into TIME-WAIT
// and exhaust the host's ephemeral port range, which then breaks unrelated
// things on the same machine: the container runtime publishes environment
// ports from that same range.
func (d *AgentDialer) maintain(ctx context.Context) {
	server := &http.Server{Handler: d.Handler, ReadHeaderTimeout: 30 * time.Second}
	backoff := minRedialDelay
	for ctx.Err() == nil {
		conn, err := d.offer(ctx)
		if err != nil {
			// A broker that is down must not be hammered.
			select {
			case <-ctx.Done():
				return
			case <-time.After(backoff):
			}
			if backoff < maxRedialDelay {
				backoff *= 2
			}
			continue
		}
		backoff = minRedialDelay
		// The connection serves requests until the platform or the network
		// ends it, then the loop offers a replacement. Retiring it after a
		// single request instead would leave the pool briefly empty under a
		// burst, which the platform cannot distinguish from a lost agent.
		server.Serve(&singleConnListener{conn: conn}) //nolint:errcheck // the listener always ends with its connection
		// Pause before replacing it. Without this an idle agent reconnects as
		// fast as the kernel allows and exhausts the port range.
		select {
		case <-ctx.Done():
			return
		case <-time.After(minRedialDelay):
		}
	}
}

// Redial pacing. The minimum bounds socket churn on an idle agent; the maximum
// keeps a down broker from being hammered while still recovering promptly.
const (
	minRedialDelay = 250 * time.Millisecond
	maxRedialDelay = 5 * time.Second
)

func (d *AgentDialer) offer(ctx context.Context) (net.Conn, error) {
	dialer := net.Dialer{Timeout: 5 * time.Second}
	conn, err := dialer.DialContext(ctx, "tcp", d.BrokerAddress)
	if err != nil {
		return nil, err
	}
	body, err := json.Marshal(registration{Token: d.Token})
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	if _, err := conn.Write(append(body, '\n')); err != nil {
		_ = conn.Close()
		return nil, err
	}
	reply := make([]byte, 3)
	_ = conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	if _, err := io.ReadFull(conn, reply); err != nil || string(reply) != "ok\n" {
		_ = conn.Close()
		return nil, errors.New("tunnel: the broker refused this agent")
	}
	_ = conn.SetReadDeadline(time.Time{})
	return conn, nil
}

// singleConnListener hands one already-established connection to an HTTP
// server and then reports exhaustion, so the server serves exactly that one
// connection and returns.
type singleConnListener struct {
	conn net.Conn
	once sync.Once
}

func (l *singleConnListener) Accept() (net.Conn, error) {
	var conn net.Conn
	l.once.Do(func() { conn = l.conn })
	if conn == nil {
		return nil, io.EOF
	}
	return conn, nil
}

func (l *singleConnListener) Close() error   { return nil }
func (l *singleConnListener) Addr() net.Addr { return l.conn.LocalAddr() }
