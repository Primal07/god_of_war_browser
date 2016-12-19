package mesh

import (
	"encoding/binary"
	"fmt"
	"io"
	"math"
)

type stBlock struct {
	Uvs struct {
		U, V []float32
	}
	Trias struct {
		X, Y, Z []float32
		Skip    []bool
	}
	Norms struct {
		X, Y, Z []float32
	}
	Blend struct {
		R, G, B, A []uint16 // actually uint8, only for marshaling
	}
	Joints                 []uint16
	DebugPos               uint32
	HasTransparentBlending bool
}

// GS use 12:4 fixed point format
// 1 << 4 = 16
const GSFixed12Point4Delimeter = 16.0
const GSFixed12Point4Delimeter1000 = 4096.0

func VifRead1(vif []byte, debug_off uint32, debugOut io.Writer) (error, []*stBlock) {
	/*
		What game send on vif:

		Stcycl wl=1 cl=1/2/3/4

		One of array:
		[ xyzw4_16i ] -
			only position (GUI)
		[ rgba4_08u , xyzw4_16i ] -
			color and position (GUI + Effects)
		[ uv2_16i , xyzw4_16i ] -
			texture coords and position (simple model)
		[ uv2_16i , rgba4_08u , xyzw4_16i ] -
			texture coords + blend for hand-shaded models + position
		[ uv2_16i , norm2_16i , rgba4_08u , xyzw4_16i ] -
			texture coords + normal vector + blend color + position for hard materials

		Stcycl wl=1 cl=1

		Command vars:
		[ xyzw4_32i ] -
			paket refrence (verticles count, joint assign, joint types).
			used stable targets: 000, 155, 2ab
		[ xyzw4_32i ] -
			material refrence ? (diffuse/ambient colors, alpha)?

		Mscall (if not last sequence in packet) - process data

		Anyway position all time xyzw4_16i and last in sequence
	*/

	result := make([]*stBlock, 0)

	var block_data_xyzw []byte = nil
	var block_data_rgba []byte = nil
	var block_data_uv []byte = nil
	block_data_uv_width := 0
	var block_data_norm []byte = nil
	var block_data_vertex_meta []byte = nil

	pos := uint32(0)
	spaces := "     "
	exit := false
	flush := false

	for iCommandInBlock := 0; !exit; iCommandInBlock++ {
		pos = ((pos + 3) / 4) * 4
		if pos >= uint32(len(vif)) {
			break
		}

		pk_cmd := vif[pos+3]
		pk_num := vif[pos+2]
		pk_dat2 := vif[pos+1]
		pk_dat1 := vif[pos]

		tagpos := pos
		pos += 4

		if pk_cmd >= 0x60 { // if unpack command
			components := ((pk_cmd >> 2) & 0x3) + 1
			bwidth := pk_cmd & 0x3
			widthmap := []uint32{32, 16, 8, 4} // 4 = r5g5b5a1
			width := widthmap[bwidth]

			blocksize := uint32(components) * ((width * uint32(pk_num)) / 8)

			signed := ((pk_dat2&(1<<6))>>6)^1 != 0
			address := (pk_dat2&(1<<7))>>7 != 0

			target := uint32(pk_dat1) | (uint32(pk_dat2&3) << 8)

			handledBy := ""

			switch width {
			case 32:
				if signed {
					switch components {
					case 4: // joints and format info all time after data (i think)
						flush = true
						handledBy = "meta"
						for i := byte(0); i < pk_num; i++ {
							bp := pos + uint32(i)*0x10
							fmt.Fprintf(debugOut, "%s -  %.6x = ", spaces, debug_off+bp)
							for i := uint32(0); i < 16; i += 4 {
								fmt.Fprintf(debugOut, "%.8x ", binary.LittleEndian.Uint32(vif[bp+i:bp+i+4]))
							}
							fmt.Fprintf(debugOut, "\n")
						}
						switch target {
						case 0x000, 0x155, 0x2ab:
							block_data_vertex_meta = vif[pos : pos+blocksize]
							handledBy = "vmta"
						default:
							for i := byte(0); i < pk_num; i++ {
								bp := pos + uint32(i)*0x10
								fmt.Fprintf(debugOut, "%s -  %.6x = ", spaces, debug_off+bp)
								for i := uint32(0); i < 16; i += 4 {
									fmt.Fprintf(debugOut, "%.f  ", math.Float32frombits(binary.LittleEndian.Uint32(vif[bp+i:bp+i+4])))
								}
								fmt.Fprintf(debugOut, "\n")
							}

						}
					case 2:
						handledBy = " uv4"
						if block_data_uv == nil {
							block_data_uv = vif[pos : pos+blocksize]
							handledBy = " uv2"
							block_data_uv_width = 4
						} else {
							return fmt.Errorf("UV already present. What is this: %.6x ?", tagpos+debug_off), nil
						}
					}
				}
			case 16:
				if signed {
					switch components {
					case 4:
						if block_data_xyzw == nil {
							block_data_xyzw = vif[pos : pos+blocksize]
							handledBy = "xyzw"
						} else {
							return fmt.Errorf("XYZW already present. What is this: %.6x ?", tagpos+debug_off), nil
						}
					case 2:
						if block_data_uv == nil {
							block_data_uv = vif[pos : pos+blocksize]
							handledBy = " uv2"
							block_data_uv_width = 2
						} else {
							return fmt.Errorf("UV already present. What is this: %.6x ?", tagpos+debug_off), nil
						}
					}
				}
			case 8:
				if signed {
					switch components {
					case 3:
						if block_data_norm == nil {
							block_data_norm = vif[pos : pos+blocksize]
							handledBy = "norm"
						} else {
							return fmt.Errorf("NORM already present. What is this: %.6x ?", tagpos+debug_off), nil
						}
					}
				} else {
					switch components {
					case 4:
						if block_data_rgba == nil {
							block_data_rgba = vif[pos : pos+blocksize]
							handledBy = "rgba"
						} else {
							return fmt.Errorf("RGBA already present. What is this: %.6x ?", tagpos+debug_off), nil
						}
					}
				}
			}

			if handledBy == "" {
				return fmt.Errorf("Block %.6x (cmd %.2x; %d bit; %d components; %d elements; sign %t; addr %t; target: %.3x; size: %.6x) not handled",
					tagpos+debug_off, pk_cmd, width, components, pk_num, signed, address, target, blocksize), nil
			} else {
				fmt.Fprintf(debugOut, "%s %.6x vif unpack [%s]: %.2x elements: %.2x components: %d type: %.2d target: %.3x sign: %t addr: %t size: %.6x\n",
					spaces, debug_off+tagpos, handledBy, pk_cmd, pk_num, components, width, target, signed, address, blocksize)
			}

			pos += blocksize
		} else {
			switch pk_cmd {
			case 0:
				fmt.Fprintf(debugOut, "%s %.6x nop\n", spaces, debug_off+tagpos)
			case 01:
				fmt.Fprintf(debugOut, "%s %.6x Stcycl wl=%.2x cl=%.2x\n", spaces, debug_off+tagpos, pk_dat2, pk_dat1)
			case 05:
				cmode := " pos "
				/*	 Decompression modes
				Normal = 0,
				OffsetDecompression, // would conflict with vif code
				Difference
				*/
				switch pk_dat1 {
				case 1:
					cmode = "[pos]"
				case 2:
					cmode = "[cur]"
				}
				fmt.Fprintf(debugOut, "%s %.6x Stmod  mode=%s (%d)\n", spaces, debug_off+tagpos, cmode, pk_dat1)
			case 0x14:
				fmt.Fprintf(debugOut, "%s %.6x Mscall proc command\n", spaces, debug_off+tagpos)
				flush = true
			case 0x30:
				fmt.Fprintf(debugOut, "%s %.6x Strow  proc command\n", spaces, debug_off+tagpos)
				pos += 0x10
			default:
				return fmt.Errorf("Unknown %.6x VIF command: %.2x:%.2x data: %.2x:%.2x",
					debug_off+tagpos, pk_cmd, pk_num, pk_dat1, pk_dat2), nil
			}
		}

		if flush || exit {
			flush = false

			// if we collect some data
			if block_data_xyzw != nil {
				currentBlock := &stBlock{HasTransparentBlending: false}
				currentBlock.DebugPos = tagpos

				countTrias := len(block_data_xyzw) / 8
				currentBlock.Trias.X = make([]float32, countTrias)
				currentBlock.Trias.Y = make([]float32, countTrias)
				currentBlock.Trias.Z = make([]float32, countTrias)
				currentBlock.Trias.Skip = make([]bool, countTrias)
				for i := range currentBlock.Trias.X {
					bp := i * 8
					currentBlock.Trias.X[i] = float32(int16(binary.LittleEndian.Uint16(block_data_xyzw[bp:bp+2]))) / GSFixed12Point4Delimeter
					currentBlock.Trias.Y[i] = float32(int16(binary.LittleEndian.Uint16(block_data_xyzw[bp+2:bp+4]))) / GSFixed12Point4Delimeter
					currentBlock.Trias.Z[i] = float32(int16(binary.LittleEndian.Uint16(block_data_xyzw[bp+4:bp+6]))) / GSFixed12Point4Delimeter
					//fmt.Fprintf(debugOut, "%.2x ", block_data_xyzw[bp+7])
					currentBlock.Trias.Skip[i] = block_data_xyzw[bp+7]&0x80 != 0
				}
				//fmt.Fprintf(debugOut, "\n")

				if block_data_uv != nil {
					switch block_data_uv_width {
					case 2:
						uvCount := len(block_data_uv) / 4
						currentBlock.Uvs.U = make([]float32, uvCount)
						currentBlock.Uvs.V = make([]float32, uvCount)
						for i := range currentBlock.Uvs.U {
							bp := i * 4
							currentBlock.Uvs.U[i] = float32(int16(binary.LittleEndian.Uint16(block_data_uv[bp:bp+2]))) / GSFixed12Point4Delimeter1000
							currentBlock.Uvs.V[i] = float32(int16(binary.LittleEndian.Uint16(block_data_uv[bp+2:bp+4]))) / GSFixed12Point4Delimeter1000
						}
					case 4:
						uvCount := len(block_data_uv) / 8
						currentBlock.Uvs.U = make([]float32, uvCount)
						currentBlock.Uvs.V = make([]float32, uvCount)
						for i := range currentBlock.Uvs.U {
							bp := i * 8
							currentBlock.Uvs.U[i] = float32(int32(binary.LittleEndian.Uint32(block_data_uv[bp:bp+4]))) / GSFixed12Point4Delimeter1000
							currentBlock.Uvs.V[i] = float32(int32(binary.LittleEndian.Uint32(block_data_uv[bp+4:bp+8]))) / GSFixed12Point4Delimeter1000
						}
					}
				}

				if block_data_norm != nil {
					normcnt := len(block_data_norm) / 3
					currentBlock.Norms.X = make([]float32, normcnt)
					currentBlock.Norms.Y = make([]float32, normcnt)
					currentBlock.Norms.Z = make([]float32, normcnt)
					for i := range currentBlock.Norms.X {
						bp := i * 3
						currentBlock.Norms.X[i] = float32(int8(block_data_norm[bp])) / 100.0
						currentBlock.Norms.Y[i] = float32(int8(block_data_norm[bp+1])) / 100.0
						currentBlock.Norms.Z[i] = float32(int8(block_data_norm[bp+2])) / 100.0
					}
				}

				if block_data_rgba != nil {
					rgbacnt := len(block_data_rgba) / 4
					currentBlock.Blend.R = make([]uint16, rgbacnt)
					currentBlock.Blend.G = make([]uint16, rgbacnt)
					currentBlock.Blend.B = make([]uint16, rgbacnt)
					currentBlock.Blend.A = make([]uint16, rgbacnt)
					for i := range currentBlock.Blend.R {
						bp := i * 4
						currentBlock.Blend.R[i] = uint16(block_data_rgba[bp])
						currentBlock.Blend.G[i] = uint16(block_data_rgba[bp+1])
						currentBlock.Blend.B[i] = uint16(block_data_rgba[bp+2])
						currentBlock.Blend.A[i] = uint16(block_data_rgba[bp+3])
					}
					for _, a := range currentBlock.Blend.A {
						if a < 0x80 {
							currentBlock.HasTransparentBlending = true
							break
						}
					}
				}

				if block_data_vertex_meta != nil {
					blocks := len(block_data_vertex_meta) / 16
					vertexes := len(currentBlock.Trias.X)

					currentBlock.Joints = make([]uint16, vertexes)

					vertnum := 0
					for i := 0; i < blocks; i++ {
						block := block_data_vertex_meta[i*16 : i*16+16]

						block_verts := int(block[0])

						for j := 0; j < block_verts; j++ {
							currentBlock.Joints[vertnum+j] = uint16(block[13] >> 4)
						}

						vertnum += block_verts

						if block[1]&0x80 != 0 {
							if i != blocks-1 {
								return fmt.Errorf("Block count != blocks: %v <= %v", blocks, i), nil
							}
						}
					}
					if vertnum != vertexes {
						return fmt.Errorf("Vertnum != vertexes count: %v <= %v", vertnum, vertexes), nil
					}
				}

				result = append(result, currentBlock)

				fmt.Fprintf(debugOut, "%s = Flush xyzw:%t, rgba:%t, uv:%t, norm:%t\n", spaces,
					block_data_xyzw != nil, block_data_rgba != nil,
					block_data_uv != nil, block_data_norm != nil)

				block_data_norm = nil
				block_data_rgba = nil
				block_data_xyzw = nil
				block_data_uv = nil

			}
		}
	}
	return nil, result
}
