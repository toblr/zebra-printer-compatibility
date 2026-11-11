package main

// PWG-Raster (PWG 5102.4) lesen und in ZPL-Grafiketiketten umwandeln.

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"strings"
)

type page struct {
	width, height int    // Druckpunkte
	bpr           int    // Bytes pro Zeile
	bits          []byte // 1 = schwarz, MSB links
}

const (
	maxRasterWidth  = 8192
	maxRasterHeight = 40000
)

// readPWG liest alle Seiten eines PWG-Raster-Datenstroms (nach der Kennung "RaS2").
func readPWG(r io.Reader, cfg *Config) ([]*page, error) {
	br := bufio.NewReaderSize(r, 1<<16)
	sync := make([]byte, 4)
	if _, err := io.ReadFull(br, sync); err != nil || string(sync) != "RaS2" {
		return nil, errors.New("kein PWG-Raster")
	}
	var pages []*page
	hdr := make([]byte, 1796)
	for {
		if _, err := io.ReadFull(br, hdr); err != nil {
			if err == io.EOF && len(pages) > 0 {
				return pages, nil
			}
			if len(pages) > 0 {
				return pages, nil
			}
			return nil, fmt.Errorf("Seitenkopf unvollstaendig: %v", err)
		}
		u := func(off int) int { return int(binary.BigEndian.Uint32(hdr[off:])) }
		w, h := u(372), u(376)
		bpp, bpl, cspace := u(388), u(392), u(400)
		if w <= 0 || h <= 0 || w > maxRasterWidth || h > maxRasterHeight {
			return nil, fmt.Errorf("unplausible Seitengroesse %dx%d", w, h)
		}
		unit := bpp / 8
		if bpp == 1 {
			unit = 1
		}
		if !(bpp == 1 || bpp == 8 || bpp == 24) || bpl < (w*bpp+7)/8 {
			return nil, fmt.Errorf("nicht unterstuetztes Rasterformat (%d Bit/Pixel, Farbraum %d)", bpp, cspace)
		}
		raw := make([]byte, bpl*h)
		line := make([]byte, bpl)
		px := make([]byte, unit)
		for y := 0; y < h; {
			rep, err := br.ReadByte()
			if err != nil {
				return nil, fmt.Errorf("Rasterdaten enden vorzeitig (Zeile %d von %d)", y, h)
			}
			for x := 0; x < bpl; {
				c, err := br.ReadByte()
				if err != nil {
					return nil, errors.New("Rasterdaten enden vorzeitig")
				}
				if c <= 127 { // Pixel (c+1)-mal wiederholen
					if _, err := io.ReadFull(br, px); err != nil {
						return nil, errors.New("Rasterdaten enden vorzeitig")
					}
					for n := int(c) + 1; n > 0 && x < bpl; n-- {
						copy(line[x:], px)
						x += unit
					}
				} else { // (257-c) Pixel woertlich
					n := (257 - int(c)) * unit
					if x+n > bpl {
						return nil, errors.New("Rasterzeile laeuft ueber")
					}
					if _, err := io.ReadFull(br, line[x:x+n]); err != nil {
						return nil, errors.New("Rasterdaten enden vorzeitig")
					}
					x += n
				}
			}
			for n := int(rep) + 1; n > 0 && y < h; n-- {
				copy(raw[y*bpl:], line)
				y++
			}
		}
		pages = append(pages, toMono(raw, w, h, bpp, bpl, cspace, cfg))
	}
}

// toMono wandelt eine Rasterseite in 1 Bit um und beschneidet auf die Druckkopfbreite.
func toMono(raw []byte, w, h, bpp, bpl, cspace int, cfg *Config) *page {
	maxW := cfg.maxDots()
	ow := w
	if ow > maxW {
		ow = maxW
	}
	p := &page{width: ow, height: h, bpr: (ow + 7) / 8}
	p.bits = make([]byte, p.bpr*h)
	if bpp == 1 {
		inv := cspace != 3 // nur bei "black" bedeutet 1 = schwarz
		for y := 0; y < h; y++ {
			src := raw[y*bpl : y*bpl+p.bpr]
			dst := p.bits[y*p.bpr : (y+1)*p.bpr]
			for i, b := range src {
				if inv {
					b = ^b
				}
				dst[i] = b
			}
			if r := ow % 8; r != 0 {
				dst[p.bpr-1] &= byte(0xff << (8 - r))
			}
		}
		return p
	}
	// Helligkeit 0..255 je Pixel
	lum := func(x, y int) int {
		if bpp == 8 {
			v := int(raw[y*bpl+x])
			if cspace == 3 { // black_8: 255 = schwarz
				return 255 - v
			}
			return v
		}
		o := y*bpl + x*3
		return (299*int(raw[o]) + 587*int(raw[o+1]) + 114*int(raw[o+2])) / 1000
	}
	thr := cfg.Threshold * 255 / 100
	if !cfg.Dither {
		for y := 0; y < h; y++ {
			for x := 0; x < ow; x++ {
				if lum(x, y) < thr {
					p.bits[y*p.bpr+x/8] |= 0x80 >> (x % 8)
				}
			}
		}
		return p
	}
	// Floyd-Steinberg fuer Fotos und Verlaeufe
	cur := make([]int, ow+2)
	next := make([]int, ow+2)
	for y := 0; y < h; y++ {
		for x := 0; x < ow; x++ {
			v := lum(x, y) + cur[x+1]/16
			out := 255
			if v < 128 {
				out = 0
				p.bits[y*p.bpr+x/8] |= 0x80 >> (x % 8)
			}
			e := v - out
			cur[x+2] += e * 7
			next[x] += e * 3
			next[x+1] += e * 5
			next[x+2] += e
		}
		cur, next = next, cur
		for i := range next {
			next[i] = 0
		}
	}
	return p
}

const hexDigits = "0123456789ABCDEF"

// zplRun schreibt einen Lauf gleicher Hex-Zeichen in ZPL-Kurzform (G-Y = 1-19, g-z = 20-400).
func zplRun(sb *strings.Builder, ch byte, n int) {
	for n > 0 {
		k := n
		if k > 419 {
			k = 419
		}
		n -= k
		if k < 3 {
			for ; k > 0; k-- {
				sb.WriteByte(ch)
			}
			continue
		}
		if k >= 20 {
			sb.WriteByte(byte('f' + k/20))
		}
		if k%20 > 0 {
			sb.WriteByte(byte('F' + k%20))
		}
		sb.WriteByte(ch)
	}
}

// zplGraphic liefert das ^GFA-Feld der Seite mit Zebra-ASCII-Kompression.
func zplGraphic(p *page) string {
	var sb strings.Builder
	total := p.bpr * p.height
	fmt.Fprintf(&sb, "^GFA,%d,%d,%d,", total, total, p.bpr)
	hexRow := make([]byte, p.bpr*2)
	var prev []byte
	for y := 0; y < p.height; y++ {
		row := p.bits[y*p.bpr : (y+1)*p.bpr]
		if prev != nil && bytes.Equal(row, prev) {
			sb.WriteByte(':')
			continue
		}
		prev = row
		for i, b := range row {
			hexRow[2*i] = hexDigits[b>>4]
			hexRow[2*i+1] = hexDigits[b&15]
		}
		// Zeilenende aus lauter Nullen bzw. lauter F abkuerzen
		end := len(hexRow)
		tail := byte(0)
		if last := hexRow[end-1]; last == '0' || last == 'F' {
			e := end
			for e > 0 && hexRow[e-1] == last {
				e--
			}
			if end-e >= 2 {
				end = e
				tail = ','
				if last == 'F' {
					tail = '!'
				}
			}
		}
		for i := 0; i < end; {
			j := i
			for j < end && hexRow[j] == hexRow[i] {
				j++
			}
			zplRun(&sb, hexRow[i], j-i)
			i = j
		}
		if tail != 0 {
			sb.WriteByte(tail)
		}
	}
	return sb.String()
}

// zplSetup: Einstellungen, die vor jedem Etikett gesendet werden.
func zplSetup(cfg *Config, widthDots, heightDots int) string {
	var sb strings.Builder
	sb.WriteString("^XA\n")
	switch cfg.MediaTracking {
	case "gap":
		sb.WriteString("^MNY\n")
	case "mark":
		sb.WriteString("^MNM\n")
	case "continuous":
		sb.WriteString("^MNN\n")
	}
	if cfg.Speed >= 2 && cfg.Speed <= 5 {
		fmt.Fprintf(&sb, "^PR%d,%d,%d\n", cfg.Speed, cfg.Speed, cfg.Speed)
	}
	fmt.Fprintf(&sb, "^PW%d\n^LL%d\n^LH0,0\n", widthDots, heightDots)
	if cfg.Rotate180 {
		sb.WriteString("^POI\n")
	} else {
		sb.WriteString("^PON\n")
	}
	return sb.String()
}

// pagesToZPL erzeugt den vollstaendigen Druckdatenstrom.
func pagesToZPL(pages []*page, copies int, cfg *Config) []byte {
	var sb strings.Builder
	if cfg.Darkness >= 0 && cfg.Darkness <= 30 {
		fmt.Fprintf(&sb, "~SD%02d\n", cfg.Darkness)
	}
	if copies < 1 {
		copies = 1
	}
	for _, p := range pages {
		sb.WriteString(zplSetup(cfg, p.width, p.height))
		sb.WriteString("^FO0,0")
		sb.WriteString(zplGraphic(p))
		sb.WriteString("^FS\n")
		fmt.Fprintf(&sb, "^PQ%d\n^XZ\n", copies)
	}
	return []byte(sb.String())
}

// testLabel: natives ZPL-Testetikett in der Standardgroesse.
func testLabel(cfg *Config) []byte {
	l := cfg.Labels[0]
	w, h := cfg.dots(l.W), cfg.dots(l.H)
	if w > cfg.maxDots() {
		w = cfg.maxDots()
	}
	var sb strings.Builder
	if cfg.Darkness >= 0 && cfg.Darkness <= 30 {
		fmt.Fprintf(&sb, "~SD%02d\n", cfg.Darkness)
	}
	sb.WriteString(zplSetup(cfg, w, h))
	fmt.Fprintf(&sb, "^FO8,8^GB%d,%d,3^FS\n", w-16, h-16)
	fmt.Fprintf(&sb, "^FO30,30^A0N,40,40^FDZebra-Bruecke: Testdruck^FS\n")
	fmt.Fprintf(&sb, "^FO30,85^A0N,28,28^FDFormat %s mm^FS\n", l.dim())
	if h > 330 {
		sb.WriteString("^FO30,140^BY2^BCN,80,Y,N,N^FDGK420D-TEST^FS\n")
	}
	sb.WriteString("^PQ1\n^XZ\n")
	return []byte(sb.String())
}
