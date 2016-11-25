package txr

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"image/png"
	"io"
	"math"

	"github.com/mogaika/god_of_war_browser/pack/wad"
	file_gfx "github.com/mogaika/god_of_war_browser/pack/wad/gfx"
	"github.com/mogaika/god_of_war_browser/utils"
)

type Texture struct {
	Magic         uint32
	GfxName       string
	PalName       string
	SubTxrName    string
	UnkCoeff      int32
	UnkMultiplier float32
	UnkFlags1     uint16
	UnkFlags2     uint16
}

const FILE_SIZE = 0x58
const TXR_MAGIC = 0x00000007

func NewFromData(fin io.ReaderAt) (*Texture, error) {
	buf := make([]byte, FILE_SIZE)
	if _, err := fin.ReadAt(buf, 0); err != nil {
		return nil, err
	}

	tex := &Texture{
		Magic:         binary.LittleEndian.Uint32(buf[0:4]),
		GfxName:       utils.BytesToString(buf[4:28]),
		PalName:       utils.BytesToString(buf[28:52]),
		SubTxrName:    utils.BytesToString(buf[52:76]),
		UnkCoeff:      int32(binary.LittleEndian.Uint32(buf[76:80])),
		UnkMultiplier: math.Float32frombits(binary.LittleEndian.Uint32(buf[80:84])),
		UnkFlags1:     binary.LittleEndian.Uint16(buf[84:86]),
		UnkFlags2:     binary.LittleEndian.Uint16(buf[86:88]),
	}

	if tex.Magic != TXR_MAGIC {
		return nil, errors.New("Wrong magic.")
	}

	if tex.UnkCoeff > 0 {
		return nil, fmt.Errorf("Unkonwn coeff %d", tex.UnkCoeff)
	}

	// 0 - any; 8000 - alpha channel
	if tex.UnkFlags1 != 0 && tex.UnkFlags1 != 0x8000 {
		return nil, fmt.Errorf("Unkonwn unkFlags1 0x%.4x != 0", tex.UnkFlags1)
	}

	// 1 - mask; 5d - alpha channel; 51 - font
	if tex.UnkFlags2 != 1 && tex.UnkFlags2 != 0x41 && tex.UnkFlags2 != 0x5d && tex.UnkFlags2 != 0x51 && tex.UnkFlags2 != 0x11 {
		return nil, fmt.Errorf("Unkonwn unkFlags2 0x%.4x (0x1,0x41,0x5d,0x51,0x11)", tex.UnkFlags2)
	}

	return tex, nil
}

func (txr *Texture) Image(gfx *file_gfx.GFX, pal *file_gfx.GFX, igfx int, ipal int) (image.Image, bool, error) {
	width := int(gfx.Width)
	height := int(gfx.Height)

	img := image.NewRGBA(image.Rect(0, 0, width, height))
	pallete, err := pal.AsPallet(ipal)

	if err != nil {
		return nil, false, err
	}

	haveTransparent := false

	palidx := gfx.AsPalletIndexes(igfx)

	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			clr := pallete[palidx[x+y*width]]
			img.Set(x, y, clr)

			if !haveTransparent {
				if _, _, _, a := clr.RGBA(); a < 0xffff {
					haveTransparent = true
				}
			}
		}
	}

	return img, haveTransparent, nil
}

type AjaxImage struct {
	Gfx, Pal int
	Image    []byte
}
type Ajax struct {
	Data            *Texture
	Images          []AjaxImage
	UsedGfx         int
	UsedPal         int
	HaveTransparent bool
}

func (txr *Texture) Marshal(wad *wad.Wad, node *wad.WadNode) (interface{}, error) {
	res := &Ajax{Data: txr, HaveTransparent: false}

	if txr.GfxName != "" && txr.PalName != "" {
		gfxn := node.FindNode(txr.GfxName)
		paln := node.FindNode(txr.PalName)
		if gfxn == nil {
			return nil, fmt.Errorf("Cannot find gfx: %s", txr.GfxName)
		}

		if paln == nil {
			return nil, fmt.Errorf("Cannot find pal: %s", txr.PalName)
		}

		res.UsedGfx = gfxn.Id
		res.UsedPal = paln.Id

		gfxc, err := wad.Get(gfxn.Id)
		if err != nil {
			return nil, fmt.Errorf("Error getting gfx %s: %v", txr.GfxName, err)
		}

		palc, err := wad.Get(paln.Id)
		if err != nil {
			return nil, fmt.Errorf("Error getting pal %s: %v", txr.PalName, err)
		}

		gfx := gfxc.(*file_gfx.GFX)
		pal := palc.(*file_gfx.GFX)

		res.Images = make([]AjaxImage, len(gfx.Data)*len(pal.Data))

		i := 0
		for iGfx := range gfx.Data {
			for iPal := range pal.Data {
				img, haveTransparent, err := txr.Image(gfx, pal, iGfx, iPal)
				if err != nil {
					return nil, err
				}

				if !res.HaveTransparent {
					res.HaveTransparent = haveTransparent
				}

				var buf bytes.Buffer
				png.Encode(&buf, img)

				res.Images[i].Gfx = iGfx
				res.Images[i].Pal = iPal
				res.Images[i].Image = buf.Bytes()

				i++
			}
		}
	}
	return res, nil
}

func init() {
	wad.SetHandler(TXR_MAGIC, func(w *wad.Wad, node *wad.WadNode, r io.ReaderAt) (wad.File, error) {
		return NewFromData(r)
	})
}
