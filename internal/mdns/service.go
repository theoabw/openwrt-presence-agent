package mdns

import (
	"encoding/binary"
	"errors"
	"strconv"
	"strings"
	"time"
)

const (
	serviceType    = "_openwrt-presence-agent._tcp.local."
	instancePrefix = "openwrt-presence-agent-"
	hostPrefix     = "openwrt-presence-"

	announcementTTL = 120
	refreshInterval = 60 * time.Second
)

// advertisement holds the names and endpoint published for one agent.
type advertisement struct {
	instance string
	host     string
	port     uint16
	address  [4]byte
	agentID  string
}

// recordSet is the complete set of records this agent publishes.
type recordSet struct {
	ptr     record
	srv     record
	txt     record
	address record
}

func (s recordSet) all() []record {
	return []record{s.ptr, s.srv, s.txt, s.address}
}

func newAdvertisement(agentID string, port int) (advertisement, error) {
	if !validAgentID(agentID) {
		return advertisement{}, errors.New("agent ID must be 32 lowercase hexadecimal characters")
	}
	if port < 1 || port > 65535 {
		return advertisement{}, errors.New("mDNS port must be between 1 and 65535")
	}
	short := agentID[:8]
	return advertisement{
		instance: instancePrefix + short + "." + serviceType,
		host:     hostPrefix + short + ".local.",
		port:     uint16(port),
		agentID:  agentID,
	}, nil
}

func validAgentID(value string) bool {
	if len(value) != 32 {
		return false
	}
	for i := 0; i < len(value); i++ {
		if (value[i] < '0' || value[i] > '9') && (value[i] < 'a' || value[i] > 'f') {
			return false
		}
	}
	return true
}

func (a advertisement) records(ttl uint32) (recordSet, error) {
	instance, err := encodeName(a.instance)
	if err != nil {
		return recordSet{}, err
	}
	host, err := encodeName(a.host)
	if err != nil {
		return recordSet{}, err
	}
	srv := make([]byte, 0, 6+len(host))
	srv = binary.BigEndian.AppendUint16(srv, 0)
	srv = binary.BigEndian.AppendUint16(srv, 0)
	srv = binary.BigEndian.AppendUint16(srv, a.port)
	srv = append(srv, host...)
	return recordSet{
		ptr: record{name: serviceType, rtype: typePTR, class: classIN, ttl: ttl, data: instance},
		srv: record{
			name: a.instance, rtype: typeSRV, class: classIN | cacheFlush,
			ttl: ttl, data: srv,
		},
		txt: record{
			name: a.instance, rtype: typeTXT, class: classIN | cacheFlush,
			ttl: ttl, data: encodeTXT(a.agentID),
		},
		address: record{
			name: a.host, rtype: typeA, class: classIN | cacheFlush,
			ttl: ttl, data: a.address[:],
		},
	}, nil
}

func encodeTXT(agentID string) []byte {
	properties := []string{"agent_id=" + agentID, "tls=false"}
	encoded := make([]byte, 0, 64)
	for _, property := range properties {
		encoded = append(encoded, byte(len(property)))
		encoded = append(encoded, property...)
	}
	return encoded
}

type queryResponse struct {
	questions   []question
	answers     []record
	additionals []record
}

func (a advertisement) responseFor(questions []question, ttl uint32) queryResponse {
	records, err := a.records(ttl)
	if err != nil {
		return queryResponse{}
	}
	var response queryResponse
	seen := make(map[string]bool)
	// Answers are collected before additionals so a record already published as
	// an answer is not repeated in the additional section.
	for _, q := range questions {
		answers, _ := records.answer(q)
		if len(answers) == 0 {
			continue
		}
		response.questions = append(response.questions, q)
		response.answers = appendUnique(response.answers, answers, seen)
	}
	for _, q := range questions {
		_, additionals := records.answer(q)
		response.additionals = appendUnique(response.additionals, additionals, seen)
	}
	return response
}

func (s recordSet) answer(q question) ([]record, []record) {
	switch {
	case q.qtype == typePTR && strings.EqualFold(q.name, serviceType):
		return []record{s.ptr}, []record{s.srv, s.txt, s.address}
	case q.qtype == typeSRV && strings.EqualFold(q.name, s.srv.name):
		return []record{s.srv}, []record{s.address}
	case q.qtype == typeTXT && strings.EqualFold(q.name, s.txt.name):
		return []record{s.txt}, nil
	case q.qtype == typeA && strings.EqualFold(q.name, s.address.name):
		return []record{s.address}, nil
	default:
		return nil, nil
	}
}

func appendUnique(target, values []record, seen map[string]bool) []record {
	for _, value := range values {
		key := value.name + ":" + strconv.FormatUint(uint64(value.rtype), 10)
		if seen[key] {
			continue
		}
		seen[key] = true
		target = append(target, value)
	}
	return target
}
