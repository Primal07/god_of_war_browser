package wad

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log"
	"math"

	"github.com/mogaika/god_of_war_browser/pack"
	"github.com/mogaika/god_of_war_browser/utils"
)

const WAD_ITEM_SIZE = 0x20

type File interface {
	Marshal(wad *Wad, node *WadNode) (interface{}, error)
}

type FileLoader func(wad *Wad, node *WadNode, r io.ReaderAt) (File, error)

var cacheHandlers map[uint32]FileLoader = make(map[uint32]FileLoader, 0)

func SetHandler(format uint32, ldr FileLoader) {
	cacheHandlers[format] = ldr
}

type Wad struct {
	Name   string
	Reader io.ReaderAt `json:"-"`
	Nodes  []*WadNode
	Roots  []int
}

func (wad *Wad) Node(id int) *WadNode {
	if id > len(wad.Nodes) || id < 0 {
		return nil
	} else {
		nd := wad.Nodes[id]
		return nd
	}
}

func (link *WadNode) ResolveLink() *WadNode {
	for link != nil && link.IsLink {
		link = link.FindNode(link.Name)
	}
	return link
}

func (wad *Wad) Get(id int) (File, error) {
	node := wad.Node(id).ResolveLink()

	if node == nil {
		return nil, errors.New("Node not found")
	}

	if node.Cache != nil {
		return node.Cache, nil
	}

	if han, ex := cacheHandlers[node.Format]; ex {
		rdr, err := wad.GetFileReader(node.Id)
		if err != nil {
			return nil, fmt.Errorf("Error getting wad '%s' node %d(%s)reader: %v", wad.Name, node.Id, node.Name, err)
		}
		cache, err := han(wad, node, rdr)
		if err == nil {
			node.Cache = cache
		}
		return cache, err
	} else {
		return nil, utils.ErrHandlerNotFound
	}
}

type WadNode struct {
	Id       int
	Name     string // can be empty
	IsLink   bool
	Parent   int
	SubNodes []int
	Flags    uint16
	Wad      *Wad `json:"-"`
	Size     uint32
	Start    int64

	Cached bool `json:"-"`
	Cache  File `json:"-"`

	Format uint32 // first 4 bytes of data
}

func (wad *Wad) NewNode(name string, isLink bool, parent int, flags uint16) *WadNode {
	node := &WadNode{
		Id:     len(wad.Nodes),
		Name:   name,
		IsLink: isLink,
		Parent: parent,
		Wad:    wad,
		Flags:  flags,
	}

	wad.Nodes = append(wad.Nodes, node)
	if parent >= 0 {
		pnode := wad.Node(parent)
		pnode.SubNodes = append(pnode.SubNodes, node.Id)
	} else {
		wad.Roots = append(wad.Roots, node.Id)
	}
	return node
}

func (wad *Wad) FindNode(name string, parent int, end_at int) *WadNode {
	var result *WadNode = nil
	if parent < 0 {
		for _, n := range wad.Roots {
			if n >= end_at {
				return result
			}
			nd := wad.Node(n)
			if nd.Name == name {
				result = nd
			}
		}
		return result
	} else {
		if wad.Nodes != nil {
			for _, n := range wad.Node(parent).SubNodes {
				if n >= end_at {
					if result != nil {
						return result
					} else {
						break
					}
				}
				nd := wad.Node(n)
				if nd.Name == name {
					result = nd
				}
			}
		}
		return wad.FindNode(name, wad.Node(parent).Parent, parent)
	}
}

func (wad *Wad) GetFileReader(id int) (*io.SectionReader, error) {
	node := wad.Node(id)
	return io.NewSectionReader(wad.Reader, node.Start, int64(node.Size)), nil
}

func (node *WadNode) FindNode(name string) *WadNode {
	return node.Wad.FindNode(name, node.Parent, node.Id)
}

func NewWad(r io.ReaderAt, name string) (*Wad, error) {
	wad := &Wad{
		Reader: r,
		Name:   name,
	}

	item := make([]byte, WAD_ITEM_SIZE)

	newGroupTag := false
	currentNode := -1

	pos := int64(0)
	for {
		_, err := r.ReadAt(item, pos)
		if err != nil {
			if err == io.EOF {
				break
			} else {
				return nil, fmt.Errorf("Error reading from wad: %v", err)
			}
		}

		tag := binary.LittleEndian.Uint16(item[0:2])
		flags := binary.LittleEndian.Uint16(item[2:4])
		size := binary.LittleEndian.Uint32(item[4:8])
		name := utils.BytesToString(item[8:32])

		switch tag {
		case 0x1e: // file data packet
			data_pos := pos + WAD_ITEM_SIZE
			var node *WadNode
			// minimal size of data == 4, for storing data format
			nd := wad.FindNode(name, currentNode, len(wad.Nodes))
			if nd != nil && nd.Parent == currentNode {
				log.Printf("Finded copy of %s->%d", nd.Name, nd.Id)
			}

			if size == 0 {
				node = wad.NewNode(name, true, currentNode, flags)
			} else {
				node = wad.NewNode(name, false, currentNode, flags)

				var bfmt [4]byte
				_, err := r.ReadAt(bfmt[:], data_pos)
				if err != nil {
					return nil, err
				}
				node.Format = binary.LittleEndian.Uint32(bfmt[0:4])
			}
			node.Size = size
			node.Start = data_pos

			if newGroupTag {
				newGroupTag = false
				currentNode = node.Id
			}
		case 0x28: // file data group start
			newGroupTag = true
		case 0x32: // file data group end
			newGroupTag = false
			if currentNode < 0 {
				return nil, errors.New("Trying to end not started group")
			} else {
				currentNode = wad.Nodes[currentNode].Parent
			}
		case 0x18: // entity count
			size = 0

		// TODO: use this tags
		case 0x006e: // MC_DATA   < R_PERM.WAD
		case 0x006f: // MC_ICON   < R_PERM.WAD
		case 0x0070: // MSH_BDepoly6Shape
		case 0x0071: // TWK_Cloth_195
		case 0x0072: // TWK_CombatFile_328
		case 0x01f4: // RSRCS
		case 0x029a: // file data start
		case 0x0378: // file header start
		case 0x03e7: // file header pop heap
		default:
			log.Printf("unknown wad tag %.4x size %.8x name %s", tag, size, name)
			return nil, fmt.Errorf("unknown tag")
		}

		off := (size + 15) & (15 ^ math.MaxUint32)
		pos = int64(off) + pos + 0x20
	}

	return wad, nil
}

func init() {
	pack.SetHandler(".WAD", func(p *pack.Pack, pf *pack.PackFile, r io.ReaderAt) (interface{}, error) {
		return NewWad(r, pf.Name)
	})
}
