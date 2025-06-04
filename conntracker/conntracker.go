package conntracker

import (
	"context"
	"net"
	"sync"
)

// ConnTracker records and tracks every conn the transport dials. A conn is
// removed from the active list when it is closed by net.Transport.
type ConnTracker struct {
	dialer *net.Dialer
	mu     sync.Mutex
	active map[net.Conn]struct{}
}

func NewConnTracker(d *net.Dialer) *ConnTracker {
	return &ConnTracker{
		dialer: d,
		active: make(map[net.Conn]struct{}),
	}
}

// Allow tinkering with the tracked active and idle pools. Not for the faint of
// heart.
//
//	// h.conntracker.Monkey(func(active map[net.Conn]struct{}) {
//	// 	updateConn := func(c net.Conn) error {
//	// 		sc, ok := c.(syscall.Conn)
//	// 		if !ok {
//	// 			return errors.New("unable to cast net.Conn to syscall.Conn")
//	// 		}
//	// 		rc, err := sc.SyscallConn()
//	// 		if err != nil {
//	// 			return errors.Wrap(err, "unable to obtain syscall.RawConn from net.Conn")
//	// 		}
//
//	// 		var operr error
//	// 		if err := rc.Control(func(fd uintptr) {
//	// 			operr = syscall.SetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_MAX_PACING_RATE, h.uploadPace)
//	// 		}); err != nil {
//	// 			return errors.Wrap(err, "unable to set SO_MAX_PACING_RATE on net.Conn")
//	// 		}
//	// 		if operr != nil {
//	// 			return errors.Wrap(operr, "unable to set SO_MAX_PACING_RATE on net.Conn")
//	// 		}
//
//	// 		return nil
//	// 	}
//
//	// 	for conn := range active {
//	// 		if err := updateConn(conn); err != nil {
//	// 			panic(errors.Wrap(err, "unable to update connection pacing rate"))
//	// 		}
//	// 	}
//	// })
func (t *ConnTracker) Monkey(do func(active map[net.Conn]struct{})) {
	t.mu.Lock()
	defer t.mu.Unlock()
	do(t.active)
}

// DialContext satisfies http.Transport.DialContext.
func (t *ConnTracker) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	conn, err := t.dialer.DialContext(ctx, network, addr)
	if err != nil {
		return nil, err
	}

	t.mu.Lock()
	t.active[conn] = struct{}{}
	t.mu.Unlock()

	// Wrap the conn so we can see when the request finishes and
	// the transport calls Close().
	return &trackedConn{Conn: conn, tracker: t}, nil
}

// forget this Conn, called by trackedConn.Close()
func (t *ConnTracker) markClosed(c net.Conn) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.active, c)
}

// Counts returns number of active Conns
func (t *ConnTracker) Counts() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.active)
}

// Wraps a net.Conn to allow tracking state.
type trackedConn struct {
	net.Conn
	tracker *ConnTracker
	once    sync.Once
}

// Notify the tracker that the Conn has been closed
func (tc *trackedConn) Close() error {
	tc.once.Do(func() { tc.tracker.markClosed(tc.Conn) })
	return tc.Conn.Close()
}
