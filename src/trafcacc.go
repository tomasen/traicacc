package trafcacc

import (
	"net"
	"strconv"
	"sync"

	log "github.com/Sirupsen/logrus"
)

const (
	buffersize          = 96
	_BackendDialTimeout = 5
)

type tag bool

const (
	// BACKEND FRONTEND role tag
	BACKEND  tag = true
	FRONTEND tag = false
)

type serv struct {
	proto string
	addr  string
	ln    net.Listener
	ta    *trafcacc
}

type trafcacc struct {
	role     tag
	atomicid uint32
	cpool    *poolc
	upool    *poolu
	epool    *poole

	// packet queue
	mux sync.RWMutex
	pq  map[uint32]*pktQueue
}

// Accelerate traffic by setup listening port and upstream
func Accelerate(l, u string, role tag) {
	t := &trafcacc{}
	t.cpool = newPoolc()
	t.upool = &poolu{}
	t.epool = &poole{}
	t.pq = make(map[uint32]*pktQueue)
	t.accelerate(l, u, role)
}

// Accelerate traffic start by flag strings
func (t *trafcacc) accelerate(l, u string, role tag) {
	// TODO: make sure this only run once
	t.role = role

	for _, e := range parse(u) {
		for p := e.portBegin; p <= e.portEnd; p++ {
			u := upstream{proto: e.proto, addr: net.JoinHostPort(e.host, strconv.Itoa(p))}
			t.upool.append(&u)
		}
	}

	if t.upool.end == 0 {
		log.Fatal("no upstreams")
	}

	for _, e := range parse(l) {
		// begin to listen
		for p := e.portBegin; p <= e.portEnd; p++ {
			// listen to lhost:lport+p
			s := serv{proto: e.proto, addr: net.JoinHostPort(e.host, strconv.Itoa(p)), ta: t}
			s.listen()
		}
	}
}

func (t *trafcacc) roleString() string {
	switch t.role {
	case BACKEND:
		return "backend"
	case FRONTEND:
		return "frontend"
	}
	return "unknown"
}
