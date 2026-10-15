package sfu

import (
	"context"
	"fmt"
	"net"
	"syscall"
)

// iceTCPMSS caps the segment size of ICE-TCP connections. Over TCP the kernel packs many small
// RTP packets into full-size segments, and a VPN path with a smaller MTU that drops them without
// an ICMP reply stalls the connection (cwnd 1, growing retransmission timeouts) while small
// packets still pass. 1200 bytes keeps segments as small as the RTP packets sent over UDP.
const iceTCPMSS = 1200

// listenICETCP opens the ICE-TCP listener; accepted connections inherit its TCP_MAXSEG.
func listenICETCP(port int) (net.Listener, error) {
	lc := net.ListenConfig{Control: func(_, _ string, c syscall.RawConn) error {
		var serr error
		if err := c.Control(func(fd uintptr) {
			serr = syscall.SetsockoptInt(int(fd), syscall.IPPROTO_TCP, syscall.TCP_MAXSEG, iceTCPMSS)
		}); err != nil {
			return err
		}
		if serr != nil {
			return fmt.Errorf("set TCP_MAXSEG: %w", serr)
		}
		return nil
	}}
	return lc.Listen(context.Background(), "tcp4", fmt.Sprintf("0.0.0.0:%d", port))
}
