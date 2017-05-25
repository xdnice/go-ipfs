package commands

import (
	"bytes"
	"crypto/rand"
	"fmt"
	"io"
	"sort"
	"strings"
	"text/tabwriter"

	cmds "github.com/ipfs/go-ipfs/commands"
	e "github.com/ipfs/go-ipfs/core/commands/e"
	"gx/ipfs/QmWdiBLZ22juGtuNceNbvvHV11zKzCaoQFMP76x2w1XDFZ/go-ipfs-cmdkit"

	ci "gx/ipfs/QmP1DfoUjiWH2ZBo1PBH6FupdBucbDepx3HpWmEY6JMUpY/go-libp2p-crypto"
	peer "gx/ipfs/QmdS9KpbDyPrieswibZhkod1oXqRwZJrUPzxCofAMWpFGq/go-libp2p-peer"
)

var KeyCmd = &cmds.Command{
	Helptext: cmdsutil.HelpText{
		Tagline: "Create and list IPNS name keypairs",
		ShortDescription: `
'ipfs key gen' generates a new keypair for usage with IPNS and 'ipfs name publish'.

  > ipfs key gen --type=rsa --size=2048 mykey
  > ipfs name publish --key=mykey QmSomeHash

'ipfs key list' lists the available keys.

  > ipfs key list
  self
  mykey
		`,
	},
	Subcommands: map[string]*cmds.Command{
		"gen":  KeyGenCmd,
		"list": KeyListCmd,
	},
}

type KeyOutput struct {
	Name string
	Id   string
}

type KeyOutputList struct {
	Keys []KeyOutput
}

var KeyGenCmd = &cmds.Command{
	Helptext: cmdsutil.HelpText{
		Tagline: "Create a new keypair",
	},
	Options: []cmdsutil.Option{
		cmdsutil.StringOption("type", "t", "type of the key to create [rsa, ed25519]"),
		cmdsutil.IntOption("size", "s", "size of the key to generate"),
	},
	Arguments: []cmdsutil.Argument{
		cmdsutil.StringArg("name", true, false, "name of key to create"),
	},
	Run: func(req cmds.Request, res cmds.Response) {
		n, err := req.InvocContext().GetNode()
		if err != nil {
			res.SetError(err, cmdsutil.ErrNormal)
			return
		}

		typ, f, err := req.Option("type").String()
		if err != nil {
			res.SetError(err, cmdsutil.ErrNormal)
			return
		}

		if !f {
			res.SetError(fmt.Errorf("please specify a key type with --type"), cmdsutil.ErrNormal)
			return
		}

		size, sizefound, err := req.Option("size").Int()
		if err != nil {
			res.SetError(err, cmdsutil.ErrNormal)
			return
		}

		name := req.Arguments()[0]
		if name == "self" {
			res.SetError(fmt.Errorf("cannot create key with name 'self'"), cmdsutil.ErrNormal)
			return
		}

		var sk ci.PrivKey
		var pk ci.PubKey

		switch typ {
		case "rsa":
			if !sizefound {
				res.SetError(fmt.Errorf("please specify a key size with --size"), cmdsutil.ErrNormal)
				return
			}

			priv, pub, err := ci.GenerateKeyPairWithReader(ci.RSA, size, rand.Reader)
			if err != nil {
				res.SetError(err, cmdsutil.ErrNormal)
				return
			}

			sk = priv
			pk = pub
		case "ed25519":
			priv, pub, err := ci.GenerateEd25519Key(rand.Reader)
			if err != nil {
				res.SetError(err, cmdsutil.ErrNormal)
				return
			}

			sk = priv
			pk = pub
		default:
			res.SetError(fmt.Errorf("unrecognized key type: %s", typ), cmdsutil.ErrNormal)
			return
		}

		err = n.Repo.Keystore().Put(name, sk)
		if err != nil {
			res.SetError(err, cmdsutil.ErrNormal)
			return
		}

		pid, err := peer.IDFromPublicKey(pk)
		if err != nil {
			res.SetError(err, cmdsutil.ErrNormal)
			return
		}

		res.SetOutput(&KeyOutput{
			Name: name,
			Id:   pid.Pretty(),
		})
	},
	Marshalers: cmds.MarshalerMap{
		cmds.Text: func(res cmds.Response) (io.Reader, error) {
			v, err := unwrapOutput(res.Output())
			if err != nil {
				return nil, err
			}

			k, ok := v.(*KeyOutput)
			if !ok {
				return nil, e.TypeErr(k, v)
			}

			return strings.NewReader(k.Id + "\n"), nil
		},
	},
	Type: KeyOutput{},
}

var KeyListCmd = &cmds.Command{
	Helptext: cmdsutil.HelpText{
		Tagline: "List all local keypairs",
	},
	Options: []cmdsutil.Option{
		cmdsutil.BoolOption("l", "Show extra information about keys."),
	},
	Run: func(req cmds.Request, res cmds.Response) {
		n, err := req.InvocContext().GetNode()
		if err != nil {
			res.SetError(err, cmdsutil.ErrNormal)
			return
		}

		keys, err := n.Repo.Keystore().List()
		if err != nil {
			res.SetError(err, cmdsutil.ErrNormal)
			return
		}

		sort.Strings(keys)

		list := make([]KeyOutput, 0, len(keys)+1)

		list = append(list, KeyOutput{Name: "self", Id: n.Identity.Pretty()})

		for _, key := range keys {
			privKey, err := n.Repo.Keystore().Get(key)
			if err != nil {
				res.SetError(err, cmdsutil.ErrNormal)
				return
			}

			pubKey := privKey.GetPublic()

			pid, err := peer.IDFromPublicKey(pubKey)
			if err != nil {
				res.SetError(err, cmdsutil.ErrNormal)
				return
			}

			list = append(list, KeyOutput{Name: key, Id: pid.Pretty()})
		}

		res.SetOutput(&KeyOutputList{list})
	},
	Marshalers: cmds.MarshalerMap{
		cmds.Text: keyOutputListMarshaler,
	},
	Type: KeyOutputList{},
}

func keyOutputListMarshaler(res cmds.Response) (io.Reader, error) {
	withId, _, _ := res.Request().Option("l").Bool()

	v, err := unwrapOutput(res.Output())
	if err != nil {
		return nil, err
	}
	list, ok := v.(*KeyOutputList)
	if !ok {
		return nil, e.TypeErr(list, v)
	}

	buf := new(bytes.Buffer)
	w := tabwriter.NewWriter(buf, 1, 2, 1, ' ', 0)
	for _, s := range list.Keys {
		if withId {
			fmt.Fprintf(w, "%s\t%s\t\n", s.Id, s.Name)
		} else {
			fmt.Fprintf(w, "%s\n", s.Name)
		}
	}
	w.Flush()
	return buf, nil
}
