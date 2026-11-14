package mdns

import (
	"encoding/binary"
	"errors"
	"strings"
)

const (
	typeA   = 1
	typePTR = 12
	typeTXT = 16
	typeSRV = 33

	classIN    = 1
	classMask  = 0x7fff
	cacheFlush = 0x8000

	headerBytes   = 12
	maxLabelBytes = 63
	maxNameBytes  = 255
	maxNameJumps  = 16
	responseFlags = 0x8400
)

var (
	errMalformedPacket  = errors.New("malformed mDNS packet")
	errNotQuery         = errors.New("mDNS packet is not a query")
	errTooManyQuestions = errors.New("mDNS query has too many questions")
)

// question is the subset of a DNS question this responder acts on.
type question struct {
	name  string
	qtype uint16
}

// record is a resource record ready to be encoded into a response.
type record struct {
	name  string
	rtype uint16
	class uint16
	ttl   uint32
	data  []byte
}

func encodeName(name string) ([]byte, error) {
	if !strings.HasSuffix(name, ".") || name == "." {
		return nil, errMalformedPacket
	}
	labels := strings.Split(strings.TrimSuffix(name, "."), ".")
	encoded := make([]byte, 0, len(name)+1)
	for _, label := range labels {
		if len(label) == 0 || len(label) > maxLabelBytes {
			return nil, errMalformedPacket
		}
		for i := 0; i < len(label); i++ {
			if label[i] <= ' ' || label[i] > '~' {
				return nil, errMalformedPacket
			}
		}
		encoded = append(encoded, byte(len(label)))
		encoded = append(encoded, label...)
	}
	encoded = append(encoded, 0)
	if len(encoded) > maxNameBytes {
		return nil, errMalformedPacket
	}
	return encoded, nil
}

func decodeName(packet []byte, offset int) (string, int, error) {
	var builder strings.Builder
	next := -1
	jumps := 0
	cursor := offset
	for {
		if cursor >= len(packet) {
			return "", 0, errMalformedPacket
		}
		length := int(packet[cursor])
		switch length & 0xc0 {
		case 0x00:
			cursor++
			if length == 0 {
				if next < 0 {
					next = cursor
				}
				return builder.String() + ".", next, nil
			}
			if cursor+length > len(packet) {
				return "", 0, errMalformedPacket
			}
			if builder.Len() > 0 {
				builder.WriteByte('.')
			}
			builder.Write(packet[cursor : cursor+length])
			if builder.Len() > maxNameBytes {
				return "", 0, errMalformedPacket
			}
			cursor += length
		case 0xc0:
			if cursor+2 > len(packet) {
				return "", 0, errMalformedPacket
			}
			if next < 0 {
				next = cursor + 2
			}
			pointer := int(binary.BigEndian.Uint16(packet[cursor:cursor+2]) & 0x3fff)
			// A pointer must reference an earlier label sequence, so requiring a
			// strictly decreasing offset also terminates decompression loops.
			if pointer >= cursor {
				return "", 0, errMalformedPacket
			}
			jumps++
			if jumps > maxNameJumps {
				return "", 0, errMalformedPacket
			}
			cursor = pointer
		default:
			return "", 0, errMalformedPacket
		}
	}
}

func parseQuery(packet []byte, maxQuestions int) ([]question, error) {
	if len(packet) < headerBytes {
		return nil, errMalformedPacket
	}
	flags := binary.BigEndian.Uint16(packet[2:4])
	if flags&0x8000 != 0 || flags&0x7800 != 0 {
		return nil, errNotQuery
	}
	count := int(binary.BigEndian.Uint16(packet[4:6]))
	if count > maxQuestions {
		return nil, errTooManyQuestions
	}
	questions := make([]question, 0, count)
	offset := headerBytes
	for i := 0; i < count; i++ {
		name, next, err := decodeName(packet, offset)
		if err != nil {
			return nil, err
		}
		if next+4 > len(packet) {
			return nil, errMalformedPacket
		}
		qtype := binary.BigEndian.Uint16(packet[next : next+2])
		qclass := binary.BigEndian.Uint16(packet[next+2:next+4]) & classMask
		if qclass == classIN {
			questions = append(questions, question{name: name, qtype: qtype})
		}
		offset = next + 4
	}
	return questions, nil
}

func buildResponse(questions []question, answers, additionals []record, limit int) []byte {
	packet := make([]byte, headerBytes, limit)
	binary.BigEndian.PutUint16(packet[2:4], responseFlags)
	var counts [4]uint16
	for _, q := range questions {
		name, err := encodeName(q.name)
		if err != nil {
			continue
		}
		entry := len(name) + 4
		if len(packet)+entry > limit {
			break
		}
		packet = append(packet, name...)
		packet = binary.BigEndian.AppendUint16(packet, q.qtype)
		packet = binary.BigEndian.AppendUint16(packet, classIN)
		counts[0]++
	}
	appendRecords := func(target *uint16, records []record) {
		for _, value := range records {
			encoded, err := encodeRecord(value)
			if err != nil {
				continue
			}
			if len(packet)+len(encoded) > limit {
				return
			}
			packet = append(packet, encoded...)
			(*target)++
		}
	}
	appendRecords(&counts[1], answers)
	appendRecords(&counts[3], additionals)
	for i, count := range counts {
		binary.BigEndian.PutUint16(packet[4+i*2:], count)
	}
	return packet
}

func encodeRecord(value record) ([]byte, error) {
	name, err := encodeName(value.name)
	if err != nil {
		return nil, err
	}
	if len(value.data) > 0xffff {
		return nil, errMalformedPacket
	}
	encoded := make([]byte, 0, len(name)+10+len(value.data))
	encoded = append(encoded, name...)
	encoded = binary.BigEndian.AppendUint16(encoded, value.rtype)
	encoded = binary.BigEndian.AppendUint16(encoded, value.class)
	encoded = binary.BigEndian.AppendUint32(encoded, value.ttl)
	encoded = binary.BigEndian.AppendUint16(encoded, uint16(len(value.data)))
	return append(encoded, value.data...), nil
}
