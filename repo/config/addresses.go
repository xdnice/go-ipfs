package config

// Addresses stores the (string) multiaddr addresses for the node.
type Addresses struct {
	Swarm      []string // listener addresses for the p2p swarm network
	Announce   []string // listener addresses to announce to the network
	NoAnnounce []string // listener addresses not to announce to the network
	API        string   // address for the local API (RPC)
	Gateway    string   // address to listen on for IPFS HTTP object gateway
}
