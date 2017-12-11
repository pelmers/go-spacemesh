package p2p2

import (
	"github.com/UnrulyOS/go-unruly/log"
	"net"
	"time"
)

// Connection manager able to dial remote endpoints
// To use this manager client  should register all callbacks
// Connections may be initiated by Dial() or by remote clients connecting to the listen address
// ConnManager includes a TCP server, and a TCP client
// It provides full duplex messaging functionality over the same tcp/ip connection
type Network interface {

	// todo: change async callbacks to us channels for comm!!!!!!
	DialTCP(address string, timeOut time.Duration) (Connection, error) // Connect to a remote node. Can send when no error.

	GetNewConnections() chan Connection
	GetClosingConnections() chan Connection
	GetConnectionErrors() chan ConnectionError
	GetIncomingMessage() chan ConnectionMessage
	GetMessageSendErrors() chan MessageSendError

	// todo: add msg sending to remote node over the connection callbacks here
}

// impl internal tpye
type networkImpl struct {
	tcpListener      net.Listener
	tcpListenAddress string // Address to open connection: localhost:9999

	newConnections     chan Connection
	closingConnections chan Connection
	connectionErrors   chan ConnectionError
	incomingMessages   chan ConnectionMessage
	messageSendErrors  chan MessageSendError
}

// Implement Network interface public channel accessors
func (n *networkImpl) GetNewConnections() chan Connection {
	return n.newConnections
}
func (n *networkImpl) GetClosingConnections() chan Connection {
	return n.closingConnections
}
func (n *networkImpl) GetConnectionErrors() chan ConnectionError {
	return n.connectionErrors
}
func (n *networkImpl) GetIncomingMessage() chan ConnectionMessage {
	return n.incomingMessages
}
func (n *networkImpl) GetMessageSendErrors() chan MessageSendError {
	return n.messageSendErrors
}

// Creates a new network
// Attempts to tcp listen on address. e.g. localhost:1234
func NewNetwork(tcpListenAddress string) (Network, error) {

	log.Info("Creating server with tcp address: %s", tcpListenAddress)

	n := &networkImpl{
		tcpListenAddress:   tcpListenAddress,
		newConnections:     make(chan Connection, 20),
		closingConnections: make(chan Connection, 20),
		connectionErrors:   make(chan ConnectionError, 20),
		incomingMessages:   make(chan ConnectionMessage, 20),
		messageSendErrors:  make(chan MessageSendError, 20),
	}

	err := n.listen()

	if err != nil {
		return nil, err
	}

	return n, nil
}

// Dial a remote server with provided time out
// address:: ip:port
// Returns established connection that local clients can send messages to or error if failed
// to establish a connection
func (n *networkImpl) DialTCP(address string, timeOut time.Duration) (Connection, error) {

	// connect via dialer so we can set tcp network params
	dialer := &net.Dialer{}
	dialer.KeepAlive = time.Duration(48 * time.Hour) // drop connections after a period of inactivity
	dialer.Timeout = time.Duration(1 * time.Minute)

	log.Info("TCP dialing %s ...", address)

	netConn, err := dialer.Dial("tcp", address)

	if err != nil {
		log.Error("Failed to tcp connect to: %s. %v", address, err)
		return nil, err
	}

	log.Info("Connected to %s...", address)
	c := newConnection(netConn, n, Local)
	return c, nil
}

// Start network server
func (n *networkImpl) listen() error {
	log.Info("Starting to listen...")
	tcpListener, err := net.Listen("tcp", n.tcpListenAddress)
	if err != nil {
		log.Error("Error starting TCP server: %v", err)
		return err
	}
	n.tcpListener = tcpListener
	go n.acceptTcp()
	return nil
}

func (n *networkImpl) acceptTcp() {
	for {
		log.Info("Waiting for incoming connections...")
		netConn, err := n.tcpListener.Accept()
		if err != nil {
			log.Warning("Failed to accept connection request: %v", err)
			return
		}

		log.Info("Got new connection...")
		c := newConnection(netConn, n, Remote)

		n.newConnections <- c

	}
}
