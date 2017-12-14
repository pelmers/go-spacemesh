package swarm

import (
	"encoding/hex"
	"github.com/UnrulyOS/go-unruly/log"
	"github.com/UnrulyOS/go-unruly/p2p2/keys"
	"github.com/UnrulyOS/go-unruly/p2p2/net"
	"github.com/UnrulyOS/go-unruly/p2p2/swarm/pb"
	"github.com/golang/protobuf/proto"
	"time"
)

// This file contain swarm internal handlers
// These should only be called from the swarms eventprocessing main loop
// or by ohter internal handlers but not from a random type or go routine

// Handle local request to register a remote node in swarm
func (s *swarmImpl) onRegisterNodeRequest(req RemoteNodeData) {

	if s.peers[req.Id] == nil {
		node, err := NewRemoteNode(req.Id, req.Ip)
		if err != nil {
			// invalid id
			return
		}

		s.peers[req.Id] = node
	}
}

// Handle local request to connect to a remote node
func (s *swarmImpl) onConnectionRequest(req RemoteNodeData) {

	var err error

	// check for existing session
	remoteNode := s.peers[req.Id]

	if remoteNode == nil {

		remoteNode, err = NewRemoteNode(req.Id, req.Ip)
		if err != nil {
			return
		}

		// store new remote node by id
		s.peers[req.Id] = remoteNode
	}

	conn := remoteNode.GetActiveConnection()
	session := remoteNode.GetAuthenticatedSession()

	if conn != nil && session != nil {
		// we have a connection with the node and an active session
		return
	}

	if conn == nil {
		conn, err = s.network.DialTCP(req.Ip, time.Duration(10*time.Second))
		if err != nil {
			// log it here
			log.Error("failed to connect to remote node on advertised ip %s", req.Ip)
			return
		}

		id := conn.Id()

		// update state with new connection
		s.peersByConnection[id] = remoteNode
		s.connections[id] = conn

		// update remote node connections
		remoteNode.GetConnections()[id] = conn
	}

	// todo: we need to handle the case that there's a non-authenticated session with the remote node
	// we need to decide if to wait for it to auth, kill it, etc....
	if session == nil || !session.IsAuthenticated() {

		// start handshake protocol
		s.handshakeProtocol.CreateSession(remoteNode)
	}
}

// callback from handshake protocol when session state changes
func (s *swarmImpl) onNewSession(data HandshakeData) {

	if data.Session().IsAuthenticated() {
		log.Info("Established new session with %s", data.RemoteNode().TcpAddress())

		// store the session
		s.allSessions[data.Session().String()] = data.Session()

		// send all messages queued for the remote node we now have a session with
		for _, msg := range s.messagesPendingSession {
			if msg.RemoteNodeId == data.RemoteNode().String() {
				// SendMessage (like any other swarm public method) is go safe
				go s.SendMessage(msg)
			}
		}
	}
}

// Local request to disconnect from a node
func (s *swarmImpl) onDisconnectionRequest(req RemoteNodeData) {
	// disconnect from node...
}

// Local request to send a message to a remote node
func (s *swarmImpl) onSendHandshakeMessage(r SendMessageReq) {

	// check for existing remote node and session
	remoteNode := s.peers[r.RemoteNodeId]

	if remoteNode == nil {
		// for now we assume messages are sent only to nodes we already know their ip address
		return
	}

	conn := remoteNode.GetActiveConnection()

	if conn == nil {
		log.Error("Expected to have a connection with remote node")
		return
	}

	conn.Send(r.Payload)

}

// Local request to send a message to a remote node
func (s *swarmImpl) onSendMessageRequest(r SendMessageReq) {

	// check for existing remote node and session
	remoteNode := s.peers[r.RemoteNodeId]

	if remoteNode == nil {
		// for now we assume messages are sent only to nodes we already know their ip address
		return
	}

	session := remoteNode.GetAuthenticatedSession()
	conn := remoteNode.GetActiveConnection()

	if session == nil || conn == nil {
		// save the message for later sending and try to connect to the node
		s.messagesPendingSession[hex.EncodeToString(r.ReqId)] = r

		// try to connect to remote node and send the message once connceted
		s.onConnectionRequest(RemoteNodeData{remoteNode.String(), remoteNode.TcpAddress()})
		return
	}

	encPayload, err := session.Encrypt(r.Payload)
	if err != nil {
		log.Error("aborting send - failed to encrypt payload: %v", err)
		return
	}

	msg := &pb.CommonMessageData{
		SessionId: session.Id(),
		Payload:   encPayload,
	}

	data, err := proto.Marshal(msg)
	if err != nil {
		log.Error("aborting send - invalid msg format %v", err)
		return
	}

	// finally - send it away!
	conn.Send(data)
}

func (s *swarmImpl) onConnectionClosed(c net.Connection) {

	// forget about this connection
	id := c.Id()
	peer := s.peersByConnection[id]
	if peer != nil {
		peer.GetConnections()[id] = nil
	}
	delete(s.connections, id)
	delete(s.peersByConnection, id)
}

func (s *swarmImpl) onRemoteClientConnected(c net.Connection) {
	// nop - a remote client connected - this is handled w message
	log.Info("Remote client connected. %s", c.Id())
}

// Processes an incoming handshake protocol message
func (s *swarmImpl) onRemoteClientHandshakeMessage(msg net.ConnectionMessage) {

	data := &pb.HandshakeData{}
	err := proto.Unmarshal(msg.Message, data)
	if err != nil {
		log.Warning("unexpected handshake message format: %v", err)
		return
	}

	connId := msg.Connection.Id()

	// check if we already know about the remote node of this connection
	sender := s.peersByConnection[connId]

	if sender == nil {
		// authenticate sender before registration
		err := s.handshakeProtocol.authenticateSenderNode(data)
		if err != nil {
			log.Error("failed to authenticate message sender %v", err)
			return
		}

		nodeKey, err := keys.NewPublicKey(data.NodePubKey)
		if err != nil {
			return
		}

		sender, err = NewRemoteNode(nodeKey.String(), data.TcpAddress)
		if err != nil {
			return
		}

		// register this remote node and its connection

		sender.GetConnections()[connId] = msg.Connection
		s.peers[sender.String()] = sender
		s.peersByConnection[connId] = sender

	}

	// demux the message to the handshake protocol handler
	s.demuxer.RouteIncomingMessage(NewIncomingMessage(sender, data.Protocol, msg.Message))
}

func (s *swarmImpl) onRemoteClientProtocolMessage(msg net.ConnectionMessage, c *pb.CommonMessageData) {
	// just find the session here
	session := s.allSessions[hex.EncodeToString(c.SessionId)]

	if session == nil || !session.IsAuthenticated() {
		log.Warning("expected to have an authenticated session with this node")
		return
	}

	remoteNode := s.peers[session.RemoteNodeId()]
	if remoteNode == nil {
		log.Warning("expected to have data about this node for an established session")
		return
	}

	decPayload, err := session.Decrypt(c.Payload)
	if err != nil {
		log.Warning("Invalid message payload. %v", err)
		return
	}

	pm := &pb.ProtocolMessage{}
	err = proto.Unmarshal(decPayload, pm)
	if err != nil {
		log.Warning("Failed to get protocol message from payload. %v", err)
		return
	}

	// todo: validate protocol message before demuxing to higher level handler
	// Use pm.Metadata .....
	// 1. authenticate author (all payload data is signed by him)
	// 2. Reject if auth timestamp is too much aparat from current local time

	s.demuxer.RouteIncomingMessage(NewIncomingMessage(remoteNode, pm.Metadata.Protocol, decPayload))

}

// Main network messages handler
// c: connection we got this message on
// msg: binary protobufs encoded data
// not go safe - called from event processing main loop
func (s *swarmImpl) onRemoteClientMessage(msg net.ConnectionMessage) {

	c := &pb.CommonMessageData{}
	err := proto.Unmarshal(msg.Message, c)
	if err != nil {
		log.Warning("Bad request - closing connection...")
		msg.Connection.Close()
		return
	}

	// route messages based on msg payload lenght
	if len(c.Payload) == 0 { // handshake messages have no enc payload
		s.onRemoteClientHandshakeMessage(msg)

	} else { // protocol messages are encrypted in payload
		s.onRemoteClientProtocolMessage(msg, c)
	}
}

// not go safe - called from event processing main loop
func (s *swarmImpl) onConnectionError(err net.ConnectionError) {
	// close the connection?
	// who to notify?
	// update remote node?
	// retry to connect to node?
}

// not go safe - called from event processing main loop
func (s *swarmImpl) onMessageSendError(err net.MessageSendError) {
	// what to do here - retry ?
}