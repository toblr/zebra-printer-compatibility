package main

// Minimaler IPP-Codec (RFC 8010): gerade genug fuer einen IPP-Everywhere-Drucker.

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
)

const (
	tagOperation   = 0x01
	tagJob         = 0x02
	tagEnd         = 0x03
	tagPrinter     = 0x04
	tagUnsupported = 0x05

	tInteger    = 0x21
	tBoolean    = 0x22
	tEnum       = 0x23
	tDateTime   = 0x31
	tResolution = 0x32
	tRange      = 0x33
	tBegCol     = 0x34
	tEndCol     = 0x37
	tText       = 0x41
	tName       = 0x42
	tKeyword    = 0x44
	tURI        = 0x45
	tURIScheme  = 0x46
	tCharset    = 0x47
	tLanguage   = 0x48
	tMime       = 0x49
	tMember     = 0x4a
	tNoValue    = 0x13
)

const (
	opPrintJob      = 0x0002
	opValidateJob   = 0x0004
	opCreateJob     = 0x0005
	opSendDocument  = 0x0006
	opCancelJob     = 0x0008
	opGetJobAttrs   = 0x0009
	opGetJobs       = 0x000a
	opGetPrinter    = 0x000b
	opCancelMyJobs  = 0x0039
	opCloseJob      = 0x003b
	opIdentify      = 0x003c
	stOK            = 0x0000
	stBadRequest    = 0x0400
	stNotFound      = 0x0406
	stNotPossible   = 0x0404
	stDocFormat     = 0x040a
	stOpUnsupported = 0x0501
	stVersion       = 0x0503
)

// ---- Anfrage ----

type ippRequest struct {
	major, minor byte
	op           uint16
	id           uint32
	// Attribute je Gruppe; Werte als rohe Bytes. Collection-Mitglieder werden uebersprungen.
	opAttrs  map[string][][]byte
	jobAttrs map[string][][]byte
}

func (r *ippRequest) str(group map[string][][]byte, name string) string {
	if v := group[name]; len(v) > 0 {
		return string(v[0])
	}
	return ""
}

func (r *ippRequest) strs(group map[string][][]byte, name string) []string {
	var out []string
	for _, v := range group[name] {
		out = append(out, string(v))
	}
	return out
}

func (r *ippRequest) integer(group map[string][][]byte, name string, def int) int {
	if v := group[name]; len(v) > 0 && len(v[0]) == 4 {
		return int(int32(binary.BigEndian.Uint32(v[0])))
	}
	return def
}

func parseIPP(rd io.Reader) (*ippRequest, error) {
	hdr := make([]byte, 8)
	if _, err := io.ReadFull(rd, hdr); err != nil {
		return nil, err
	}
	req := &ippRequest{major: hdr[0], minor: hdr[1], op: binary.BigEndian.Uint16(hdr[2:]), id: binary.BigEndian.Uint32(hdr[4:]),
		opAttrs: map[string][][]byte{}, jobAttrs: map[string][][]byte{}}
	var group map[string][][]byte
	name := ""
	depth := 0
	one := make([]byte, 1)
	for n := 0; n < 10000; n++ {
		if _, err := io.ReadFull(rd, one); err != nil {
			return nil, err
		}
		tag := one[0]
		if tag == tagEnd {
			return req, nil
		}
		if tag < 0x10 { // Gruppenbeginn
			switch tag {
			case tagOperation:
				group = req.opAttrs
			case tagJob:
				group = req.jobAttrs
			default:
				group = map[string][][]byte{}
			}
			name, depth = "", 0
			continue
		}
		lb := make([]byte, 2)
		if _, err := io.ReadFull(rd, lb); err != nil {
			return nil, err
		}
		nb := make([]byte, binary.BigEndian.Uint16(lb))
		if _, err := io.ReadFull(rd, nb); err != nil {
			return nil, err
		}
		if _, err := io.ReadFull(rd, lb); err != nil {
			return nil, err
		}
		val := make([]byte, binary.BigEndian.Uint16(lb))
		if _, err := io.ReadFull(rd, val); err != nil {
			return nil, err
		}
		if group == nil {
			return nil, errors.New("Attribut ausserhalb einer Gruppe")
		}
		switch {
		case tag == tBegCol:
			if depth == 0 && len(nb) > 0 {
				name = string(nb)
			}
			depth++
		case tag == tEndCol:
			if depth > 0 {
				depth--
			}
		case depth > 0: // Mitglieder einer Collection werden nicht ausgewertet
		default:
			if len(nb) > 0 {
				name = string(nb)
			}
			if name != "" {
				group[name] = append(group[name], val)
			}
		}
	}
	return nil, errors.New("zu viele Attribute")
}

// ---- Antwort ----

type ippValue struct {
	tag  byte
	data []byte
	col  []ippAttr // bei tBegCol
}

type ippAttr struct {
	name   string
	values []ippValue
}

func vInt(tag byte, n int) ippValue {
	b := make([]byte, 4)
	binary.BigEndian.PutUint32(b, uint32(int32(n)))
	return ippValue{tag: tag, data: b}
}
func vStr(tag byte, s string) ippValue { return ippValue{tag: tag, data: []byte(s)} }
func vBool(v bool) ippValue {
	if v {
		return ippValue{tag: tBoolean, data: []byte{1}}
	}
	return ippValue{tag: tBoolean, data: []byte{0}}
}
func vRange(lo, hi int) ippValue {
	b := make([]byte, 8)
	binary.BigEndian.PutUint32(b, uint32(int32(lo)))
	binary.BigEndian.PutUint32(b[4:], uint32(int32(hi)))
	return ippValue{tag: tRange, data: b}
}
func vRes(dpi int) ippValue {
	b := make([]byte, 9)
	binary.BigEndian.PutUint32(b, uint32(dpi))
	binary.BigEndian.PutUint32(b[4:], uint32(dpi))
	b[8] = 3 // Punkte pro Zoll
	return ippValue{tag: tResolution, data: b}
}
func vCol(members ...ippAttr) ippValue { return ippValue{tag: tBegCol, col: members} }

func aInt(name string, n ...int) ippAttr {
	a := ippAttr{name: name}
	for _, v := range n {
		a.values = append(a.values, vInt(tInteger, v))
	}
	return a
}
func aEnum(name string, n ...int) ippAttr {
	a := ippAttr{name: name}
	for _, v := range n {
		a.values = append(a.values, vInt(tEnum, v))
	}
	return a
}
func aStr(tag byte, name string, s ...string) ippAttr {
	a := ippAttr{name: name}
	for _, v := range s {
		a.values = append(a.values, vStr(tag, v))
	}
	return a
}
func aKw(name string, s ...string) ippAttr    { return aStr(tKeyword, name, s...) }
func aVal(name string, v ...ippValue) ippAttr { return ippAttr{name: name, values: v} }

func writeTLV(buf *bytes.Buffer, tag byte, name string, data []byte) {
	buf.WriteByte(tag)
	binary.Write(buf, binary.BigEndian, uint16(len(name)))
	buf.WriteString(name)
	binary.Write(buf, binary.BigEndian, uint16(len(data)))
	buf.Write(data)
}

func writeValue(buf *bytes.Buffer, name string, v ippValue) {
	if v.tag != tBegCol {
		writeTLV(buf, v.tag, name, v.data)
		return
	}
	writeTLV(buf, tBegCol, name, nil)
	for _, m := range v.col {
		for i, mv := range m.values {
			if i == 0 {
				writeTLV(buf, tMember, "", []byte(m.name))
			}
			writeValue(buf, "", mv)
		}
	}
	writeTLV(buf, tEndCol, "", nil)
}

func writeAttr(buf *bytes.Buffer, a ippAttr) {
	for i, v := range a.values {
		n := ""
		if i == 0 {
			n = a.name
		}
		writeValue(buf, n, v)
	}
}

type ippGroup struct {
	tag   byte
	attrs []ippAttr
}

func buildResponse(req *ippRequest, status uint16, groups ...ippGroup) []byte {
	buf := &bytes.Buffer{}
	major, minor := byte(1), byte(1)
	if req != nil {
		major, minor = req.major, req.minor
		if major > 2 {
			major, minor = 2, 0
		}
	}
	buf.WriteByte(major)
	buf.WriteByte(minor)
	binary.Write(buf, binary.BigEndian, status)
	var id uint32
	if req != nil {
		id = req.id
	}
	binary.Write(buf, binary.BigEndian, id)
	buf.WriteByte(tagOperation)
	writeAttr(buf, aStr(tCharset, "attributes-charset", "utf-8"))
	writeAttr(buf, aStr(tLanguage, "attributes-natural-language", "en"))
	for _, g := range groups {
		buf.WriteByte(g.tag)
		for _, a := range g.attrs {
			if len(a.values) > 0 {
				writeAttr(buf, a)
			}
		}
	}
	buf.WriteByte(tagEnd)
	return buf.Bytes()
}
