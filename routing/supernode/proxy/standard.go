package proxy

import (
	"context"
	"errors"

	pstore "gx/ipfs/QmNUVzEjq3XWJ89hegahPvyfJbTXgTaom48pLb7YBD9gHQ/go-libp2p-peerstore"
	logging "gx/ipfs/QmSpJByNKFX1sCsHBEp3R73FL4NF6FnQTEGyNAXHm2GS52/go-log"
	dhtpb "gx/ipfs/QmUJKdWyaf2dpuACw7ctu3KNciyzR7S69yGFr2BP6vYUB8/go-libp2p-kad-dht/pb"
	inet "gx/ipfs/QmVHSBsn8LEeay8m5ERebgUVuhzw838PsyTttCmP6GMJkg/go-libp2p-net"
	loggables "gx/ipfs/QmVesPmqbPp7xRGyY96tnBwzDtVV1nqv4SCVxo5zCqKyH8/go-libp2p-loggables"
	kbucket "gx/ipfs/QmXKSwZVoHCTne4jTLzDtMc2K6paEZ2QaUMQfJ4ogYd28n/go-libp2p-kbucket"
	ggio "gx/ipfs/QmZ4Qi3GaRbjcx28Sme5eMH7RQjGkt8wHxt2a65oLaeFEV/gogo-protobuf/io"
	host "gx/ipfs/QmcyNeWPsoFGxThGpV8JnJdfUNankKhWCTrbrcFRQda4xR/go-libp2p-host"
	peer "gx/ipfs/QmdS9KpbDyPrieswibZhkod1oXqRwZJrUPzxCofAMWpFGq/go-libp2p-peer"
)

const ProtocolSNR = "/ipfs/supernoderouting"

var log = logging.Logger("supernode/proxy")

type Proxy interface {
	Bootstrap(context.Context) error
	HandleStream(inet.Stream)
	SendMessage(ctx context.Context, m *dhtpb.Message) error
	SendRequest(ctx context.Context, m *dhtpb.Message) (*dhtpb.Message, error)
}

type standard struct {
	Host host.Host

	remoteInfos []pstore.PeerInfo // addr required for bootstrapping
	remoteIDs   []peer.ID         // []ID is required for each req. here, cached for performance.
}

func Standard(h host.Host, remotes []pstore.PeerInfo) Proxy {
	var ids []peer.ID
	for _, remote := range remotes {
		ids = append(ids, remote.ID)
	}
	return &standard{h, remotes, ids}
}

func (px *standard) Bootstrap(ctx context.Context) error {
	var cxns []pstore.PeerInfo
	for _, info := range px.remoteInfos {
		if err := px.Host.Connect(ctx, info); err != nil {
			continue
		}
		cxns = append(cxns, info)
	}
	if len(cxns) == 0 {
		log.Error("unable to bootstrap to any supernode routers")
	} else {
		log.Infof("bootstrapped to %d supernode routers: %s", len(cxns), cxns)
	}
	return nil
}

func (p *standard) HandleStream(s inet.Stream) {
	// TODO(brian): Should clients be able to satisfy requests?
	log.Error("supernode client received (dropped) a routing message from", s.Conn().RemotePeer())
	s.Close()
}

const replicationFactor = 2

// SendMessage sends message to each remote sequentially (randomized order),
// stopping after the first successful response. If all fail, returns the last
// error.
func (px *standard) SendMessage(ctx context.Context, m *dhtpb.Message) error {
	var err error
	var numSuccesses int
	for _, remote := range sortedByKey(px.remoteIDs, m.GetKey()) {
		if err = px.sendMessage(ctx, m, remote); err != nil { // careful don't re-declare err!
			continue
		}
		numSuccesses++
		switch m.GetType() {
		case dhtpb.Message_ADD_PROVIDER, dhtpb.Message_PUT_VALUE:
			if numSuccesses < replicationFactor {
				continue
			}
		}
		return nil // success
	}
	return err // NB: returns the last error
}

func (px *standard) sendMessage(ctx context.Context, m *dhtpb.Message, remote peer.ID) (err error) {
	e := log.EventBegin(ctx, "sendRoutingMessage", px.Host.ID(), remote, m)
	defer func() {
		if err != nil {
			e.SetError(err)
		}
		e.Done()
	}()
	if err = px.Host.Connect(ctx, pstore.PeerInfo{ID: remote}); err != nil {
		return err
	}
	s, err := px.Host.NewStream(ctx, remote, ProtocolSNR)
	if err != nil {
		return err
	}
	defer s.Close()
	pbw := ggio.NewDelimitedWriter(s)
	if err := pbw.WriteMsg(m); err != nil {
		return err
	}
	return nil
}

// SendRequest sends the request to each remote sequentially (randomized order),
// stopping after the first successful response. If all fail, returns the last
// error.
func (px *standard) SendRequest(ctx context.Context, m *dhtpb.Message) (*dhtpb.Message, error) {
	var err error
	for _, remote := range sortedByKey(px.remoteIDs, m.GetKey()) {
		var reply *dhtpb.Message
		reply, err = px.sendRequest(ctx, m, remote) // careful don't redeclare err!
		if err != nil {
			continue
		}
		return reply, nil // success
	}
	return nil, err // NB: returns the last error
}

func (px *standard) sendRequest(ctx context.Context, m *dhtpb.Message, remote peer.ID) (*dhtpb.Message, error) {
	e := log.EventBegin(ctx, "sendRoutingRequest", px.Host.ID(), remote, logging.Pair("request", m))
	defer e.Done()
	if err := px.Host.Connect(ctx, pstore.PeerInfo{ID: remote}); err != nil {
		e.SetError(err)
		return nil, err
	}
	s, err := px.Host.NewStream(ctx, remote, ProtocolSNR)
	if err != nil {
		e.SetError(err)
		return nil, err
	}
	defer s.Close()
	r := ggio.NewDelimitedReader(s, inet.MessageSizeMax)
	w := ggio.NewDelimitedWriter(s)
	if err = w.WriteMsg(m); err != nil {
		e.SetError(err)
		return nil, err
	}

	response := &dhtpb.Message{}
	if err = r.ReadMsg(response); err != nil {
		e.SetError(err)
		return nil, err
	}
	// need ctx expiration?
	if response == nil {
		err := errors.New("no response to request")
		e.SetError(err)
		return nil, err
	}
	e.Append(logging.Pair("response", response))
	e.Append(logging.Pair("uuid", loggables.Uuid("foo")))
	return response, nil
}

func sortedByKey(peers []peer.ID, skey string) []peer.ID {
	target := kbucket.ConvertKey(skey)
	return kbucket.SortClosestPeers(peers, target)
}
