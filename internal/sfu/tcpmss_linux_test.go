package sfu

import (
	"net"
	"syscall"
	"testing"
)

func TestICETCPListenerCapsSegmentSize(t *testing.T) {
	ln, err := listenICETCP(0)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	client, err := net.Dial("tcp4", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	conn, err := ln.Accept()
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	raw, err := conn.(*net.TCPConn).SyscallConn()
	if err != nil {
		t.Fatal(err)
	}
	var mss int
	var serr error
	if err := raw.Control(func(fd uintptr) {
		mss, serr = syscall.GetsockoptInt(int(fd), syscall.IPPROTO_TCP, syscall.TCP_MAXSEG)
	}); err != nil {
		t.Fatal(err)
	}
	if serr != nil {
		t.Fatal(serr)
	}
	// Loopback would otherwise give ~65 KB segments.
	if mss <= 0 || mss > iceTCPMSS {
		t.Fatalf("accepted connection MSS = %d, want 1..%d", mss, iceTCPMSS)
	}
}
