package trafcacc

import (
	"encoding/gob"
	"errors"
	"math/rand"
	"net"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/Sirupsen/logrus"
)

type dialer struct {
	*node

	identity uint32
	atomicid uint32

	udpbuf []byte
}

func newDialer() *dialer {
	return &dialer{
		identity: rand.Uint32(),
		node:     newNode("dialer"),
	}
}

// Setup upstream servers
func (d *dialer) Setup(server string) {
	for _, e := range parse(server) {
		grp := 0
		for p := e.portBegin; p <= e.portEnd; p++ {
			u := newUpstream(e.proto)
			u.addr = net.JoinHostPort(e.host, strconv.Itoa(p))
			d.pool.append(u, grp)
			go d.connect(u)
		}
		grp++
	}
}

// Dial acts like net.Dial
func (d *dialer) Dial() (net.Conn, error) {
	return d.DialTimeout(time.Duration(0))
}

// DialTimeout is the maximum amount of time a dial will wait for
// a connect to complete. If Deadline is also set, it may fail
// earlier.
//
// The default is 0 means no timeout.
//
func (d *dialer) DialTimeout(timeout time.Duration) (net.Conn, error) {
	// wait for upstream online and alive
	ch := make(chan struct{}, 1)
	go func() {
		d.pool.waitforalive()

		ch <- struct{}{}
	}()
	if timeout == time.Duration(0) {
		<-ch
	} else {
		select {
		case <-ch:
		case <-time.After(timeout):
			return nil, errors.New("i/o timeout")
		}
	}

	conn := newConn(d, d.identity, atomic.AddUint32(&d.atomicid, 1))

	d.pqs.create(conn.senderid, conn.connid)

	// send connect cmd
	d.write(&packet{
		Senderid: d.identity,
		Connid:   conn.connid,
		Cmd:      connect,
		Time:     time.Now().UnixNano(),
	})

	return conn, nil
}

// connect to upstream server and keep tunnel alive
func (d *dialer) connect(u *upstream) {
	for {
		conn, err := net.Dial(u.proto, u.addr)
		if err != nil {
			logrus.WithFields(logrus.Fields{
				"addr":  u.addr,
				"error": err,
			}).Warnln("Dialer dial upstream error")
			time.Sleep(time.Second)
			continue
		}

		u.conn = conn

		switch u.proto {
		case tcp:
			u.encoder = gob.NewEncoder(conn)
			u.decoder = gob.NewDecoder(conn)
		case udp:
		}

		atomic.StoreInt32(&u.closed, 0)

		// begin to ping
		go d.pingloop(u)

		atomic.StoreInt64(&u.alive, time.Now().UnixNano())

		d.readloop(u)

		u.close()
	}
}

func (d *dialer) readloop(u *upstream) {
	for {
		if atomic.LoadInt32(&u.closed) != 0 {
			logrus.WithField("proto", u.proto).Warnln("dialer upstream is closed")
			return
		}
		p := packet{}
		switch u.proto {
		case tcp:
			err := u.decoder.Decode(&p)
			if err != nil {
				logrus.WithFields(logrus.Fields{
					"error": err,
				}).Warnln("Dialer docode upstream packet")
				return
			}
		case udp:
			udpbuf := make([]byte, buffersize)
			n, err := u.conn.Read(udpbuf)
			if err != nil {
				logrus.WithError(err).Warnln("dialer Read UDP error")
				return
			}
			if err := decodePacket(udpbuf[:n], &p); err != nil {
				logrus.WithError(err).Warnln("dialer gop decode from udp error")
				continue
			}
			p.udp = true
		}

		d.proc(u, &p)
	}
}

func (d *dialer) proc(u *upstream, p *packet) {
	d.node.proc(u, p)
	if p.Cmd == data {
		go d.push(p)
	}
}

func (d *dialer) pingloop(u *upstream) {
	ch := time.Tick(time.Second)
	for {
		err := u.send(ping)
		if err != nil {
			u.close()
			break
		}
		<-ch
	}
}
