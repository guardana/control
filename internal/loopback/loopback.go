package loopback

import (
	"errors"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"strconv"
)

// Check refuses addr unless it is host:port whose host is a loopback IP
// literal and whose port is a decimal number of at most 16 bits, spelled
// without a sign or a leading zero. Port 0 is admitted: it asks the system
// for a free port on the loopback. Every refusal names the loopback.
func Check(addr string) error {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("%s is not host:port with a loopback IP address, such as 127.0.0.1:4318", strconv.Quote(addr))
	}
	ip, err := netip.ParseAddr(host)
	if err != nil || !ip.IsLoopback() {
		return fmt.Errorf("%s is not a loopback IP address", strconv.Quote(host))
	}
	if n, err := strconv.ParseUint(port, 10, 16); err != nil || strconv.FormatUint(n, 10) != port {
		return fmt.Errorf("%s names no port on the loopback", strconv.Quote(addr))
	}
	return nil
}

// defaultPorts is the port a URL of each scheme reaches when it names none.
var defaultPorts = map[string]string{"http": "80", "https": "443"}

// CheckURL refuses raw unless it is an http or https URL whose host and
// port, the scheme's port where it names none, Check admits. A refusal
// quotes at most the host and port: the rest of a URL may carry a
// credential.
func CheckURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || defaultPorts[u.Scheme] == "" || u.Host == "" {
		return errors.New("not an http or https URL with a loopback IP address as its host")
	}
	port := u.Port()
	if port == "" {
		port = defaultPorts[u.Scheme]
	}
	return Check(net.JoinHostPort(u.Hostname(), port))
}
