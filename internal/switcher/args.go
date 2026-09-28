// Package switcher runs one llama.cpp router per runtime behind one address,
// and sends each request to the router that owns its model.
package switcher

import (
	"fmt"
	"strconv"
	"strings"
)

// ServeArgs are the arguments of "llama serve", split into the address that
// the client uses and everything else.
type ServeArgs struct {
	Host string
	Port int
	Rest []string // every other argument, in order, passed on to each router
}

// ParseServeArgs reads the arguments after "serve". llama.cpp's defaults
// apply when --host or --port is missing.
func ParseServeArgs(args []string) (ServeArgs, error) {
	a := ServeArgs{Host: "127.0.0.1", Port: 8080}
	rest := args
	for len(rest) > 0 {
		arg := rest[0]
		rest = rest[1:]
		name, value, inline := strings.Cut(arg, "=")
		if name != "--host" && name != "--port" {
			a.Rest = append(a.Rest, arg)
			continue
		}
		if !inline {
			if len(rest) == 0 {
				return a, fmt.Errorf("%s needs a value", name)
			}
			value, rest = rest[0], rest[1:]
		}
		if err := a.setAddress(name, value); err != nil {
			return a, err
		}
	}
	return a, nil
}

func (a *ServeArgs) setAddress(name, value string) error {
	if name == "--host" {
		a.Host = value
		return nil
	}
	p, err := strconv.Atoi(value)
	if err != nil || p <= 0 || p > 65535 {
		return fmt.Errorf("invalid --port %q", value)
	}
	a.Port = p
	return nil
}

// Address is where the switcher listens.
func (a ServeArgs) Address() string { return joinHostPort(a.Host, a.Port) }

// ForBackend returns the arguments for one router on a private local port.
func (a ServeArgs) ForBackend(port int) []string {
	out := append([]string(nil), a.Rest...)
	return append(out, "--host", "127.0.0.1", "--port", strconv.Itoa(port))
}

func joinHostPort(host string, port int) string {
	if strings.Contains(host, ":") && !strings.HasPrefix(host, "[") {
		host = "[" + host + "]"
	}
	return host + ":" + strconv.Itoa(port)
}
