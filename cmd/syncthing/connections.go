// Copyright (C) 2015 The Syncthing Authors.
//
// This program is free software: you can redistribute it and/or modify it
// under the terms of the GNU General Public License as published by the Free
// Software Foundation, either version 3 of the License, or (at your option)
// any later version.
//
// This program is distributed in the hope that it will be useful, but WITHOUT
// ANY WARRANTY; without even the implied warranty of MERCHANTABILITY or
// FITNESS FOR A PARTICULAR PURPOSE. See the GNU General Public License for
// more details.
//
// You should have received a copy of the GNU General Public License along
// with this program. If not, see <http://www.gnu.org/licenses/>.

package main

import (
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"strings"
	"time"

	"github.com/syncthing/protocol"
	"github.com/syncthing/syncthing/internal/events"
	"github.com/syncthing/syncthing/internal/model"
)

func listenConnect(myID protocol.DeviceID, m *model.Model, tlsCfg *tls.Config) {
	var conns = make(chan *tls.Conn)

	// Listen
	for _, addr := range cfg.Options().ListenAddress {
		go listenTLS(conns, addr, tlsCfg)
	}

	// Connect
	go dialTLS(m, conns, tlsCfg)

next:
	for conn := range conns {
		certs := conn.ConnectionState().PeerCertificates
		if cl := len(certs); cl != 1 {
			l.Infof("Got peer certificate list of length %d != 1 from %s; protocol error", cl, conn.RemoteAddr())
			conn.Close()
			continue
		}
		remoteCert := certs[0]
		remoteID := protocol.NewDeviceID(remoteCert.Raw)

		if remoteID == myID {
			l.Infof("Connected to myself (%s) - should not happen", remoteID)
			conn.Close()
			continue
		}

		if m.ConnectedTo(remoteID) {
			l.Infof("Connected to already connected device (%s)", remoteID)
			conn.Close()
			continue
		}

		for deviceID, deviceCfg := range cfg.Devices() {
			if deviceID == remoteID {
				// Verify the name on the certificate. By default we set it to
				// "syncthing" when generating, but the user may have replaced
				// the certificate and used another name.
				certName := deviceCfg.CertName
				if certName == "" {
					certName = tlsDefaultCommonName
				}
				err := remoteCert.VerifyHostname(certName)
				if err != nil {
					// Incorrect certificate name is something the user most
					// likely wants to know about, since it's an advanced
					// config. Warn instead of Info.
					l.Warnf("Bad certificate from %s (%v): %v", remoteID, conn.RemoteAddr(), err)
					conn.Close()
					continue next
				}

				// If rate limiting is set, we wrap the connection in a
				// limiter.
				wr := io.Writer(conn)
				if writeRateLimit != nil {
					wr = &limitedWriter{conn, writeRateLimit}
				}

				rd := io.Reader(conn)
				if readRateLimit != nil {
					rd = &limitedReader{conn, readRateLimit}
				}

				name := fmt.Sprintf("%s-%s", conn.LocalAddr(), conn.RemoteAddr())
				protoConn := protocol.NewConnection(remoteID, rd, wr, m, name, deviceCfg.Compression)

				l.Infof("Established secure connection to %s at %s", remoteID, name)
				if debugNet {
					l.Debugf("cipher suite %04X", conn.ConnectionState().CipherSuite)
				}
				events.Default.Log(events.DeviceConnected, map[string]string{
					"id":   remoteID.String(),
					"addr": conn.RemoteAddr().String(),
				})

				m.AddConnection(conn, protoConn)
				continue next
			}
		}

		if !cfg.IgnoredDevice(remoteID) {
			events.Default.Log(events.DeviceRejected, map[string]string{
				"device":  remoteID.String(),
				"address": conn.RemoteAddr().String(),
			})
			l.Infof("Connection from %s with unknown device ID %s", conn.RemoteAddr(), remoteID)
		} else {
			l.Infof("Connection from %s with ignored device ID %s", conn.RemoteAddr(), remoteID)
		}

		conn.Close()
	}
}

func listenTLS(conns chan *tls.Conn, addr string, tlsCfg *tls.Config) {
	if debugNet {
		l.Debugln("listening on", addr)
	}

	tcaddr, err := net.ResolveTCPAddr("tcp", addr)
	if err != nil {
		l.Fatalln("listen (BEP):", err)
	}
	listener, err := net.ListenTCP("tcp", tcaddr)
	if err != nil {
		l.Fatalln("listen (BEP):", err)
	}

	for {
		conn, err := listener.Accept()
		if err != nil {
			l.Warnln("Accepting connection:", err)
			continue
		}

		if debugNet {
			l.Debugln("connect from", conn.RemoteAddr())
		}

		tcpConn := conn.(*net.TCPConn)
		setTCPOptions(tcpConn)

		tc := tls.Server(conn, tlsCfg)
		err = tc.Handshake()
		if err != nil {
			l.Infoln("TLS handshake:", err)
			tc.Close()
			continue
		}

		conns <- tc
	}

}

func dialTLS(m *model.Model, conns chan *tls.Conn, tlsCfg *tls.Config) {
	delay := time.Second
	for {
	nextDevice:
		for deviceID, deviceCfg := range cfg.Devices() {
			if deviceID == myID {
				continue
			}

			if m.ConnectedTo(deviceID) {
				continue
			}

			var addrs []string
			for _, addr := range deviceCfg.Addresses {
				if addr == "dynamic" {
					if discoverer != nil {
						t := discoverer.Lookup(deviceID)
						if len(t) == 0 {
							continue
						}
						addrs = append(addrs, t...)
					}
				} else {
					addrs = append(addrs, addr)
				}
			}

			for _, addr := range addrs {
				host, port, err := net.SplitHostPort(addr)
				if err != nil && strings.HasPrefix(err.Error(), "missing port") {
					// addr is on the form "1.2.3.4"
					addr = net.JoinHostPort(addr, "22000")
				} else if err == nil && port == "" {
					// addr is on the form "1.2.3.4:"
					addr = net.JoinHostPort(host, "22000")
				}
				if debugNet {
					l.Debugln("dial", deviceCfg.DeviceID, addr)
				}

				raddr, err := net.ResolveTCPAddr("tcp", addr)
				if err != nil {
					if debugNet {
						l.Debugln(err)
					}
					continue
				}

				conn, err := net.DialTCP("tcp", nil, raddr)
				if err != nil {
					if debugNet {
						l.Debugln(err)
					}
					continue
				}

				setTCPOptions(conn)

				tc := tls.Client(conn, tlsCfg)
				err = tc.Handshake()
				if err != nil {
					l.Infoln("TLS handshake:", err)
					tc.Close()
					continue
				}

				conns <- tc
				continue nextDevice
			}
		}

		time.Sleep(delay)
		delay *= 2
		if maxD := time.Duration(cfg.Options().ReconnectIntervalS) * time.Second; delay > maxD {
			delay = maxD
		}
	}
}

func setTCPOptions(conn *net.TCPConn) {
	var err error
	if err = conn.SetLinger(0); err != nil {
		l.Infoln(err)
	}
	if err = conn.SetNoDelay(false); err != nil {
		l.Infoln(err)
	}
	if err = conn.SetKeepAlivePeriod(60 * time.Second); err != nil {
		l.Infoln(err)
	}
	if err = conn.SetKeepAlive(true); err != nil {
		l.Infoln(err)
	}
}
