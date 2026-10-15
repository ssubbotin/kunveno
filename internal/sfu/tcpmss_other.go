//go:build !linux

package sfu

import "net"

// listenICETCP opens the ICE-TCP listener; the segment size cap is applied on Linux only.
func listenICETCP(port int) (net.Listener, error) {
	return net.ListenTCP("tcp4", &net.TCPAddr{IP: net.IPv4zero, Port: port})
}
