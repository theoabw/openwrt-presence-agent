package mdns

import (
	"encoding/binary"
	"errors"
	"net"
	"strings"
	"testing"
)

const testAgentID = "0123456789abcdef0123456789abcdef"

func testAdvertisement(t *testing.T) advertisement {
	t.Helper()
	ad, err := newAdvertisement(testAgentID, 8787)
	if err != nil {
		t.Fatal(err)
	}
	copy(ad.address[:], []byte{192, 168, 1, 2})
	return ad
}

func buildTestQuery(t *testing.T, name string, qtype uint16) []byte {
	t.Helper()
	encoded, err := encodeName(name)
	if err != nil {
		t.Fatal(err)
	}
	packet := make([]byte, headerBytes, headerBytes+len(encoded)+4)
	binary.BigEndian.PutUint16(packet[4:6], 1)
	packet = append(packet, encoded...)
	packet = binary.BigEndian.AppendUint16(packet, qtype)
	return binary.BigEndian.AppendUint16(packet, classIN)
}

type testRecord struct {
	name  string
	rtype uint16
	class uint16
	ttl   uint32
	data  []byte
}

type testMessage struct {
	response    bool
	questions   []question
	answers     []testRecord
	additionals []testRecord
}

func parseTestMessage(packet []byte) (testMessage, error) {
	if len(packet) < headerBytes {
		return testMessage{}, errMalformedPacket
	}
	message := testMessage{response: binary.BigEndian.Uint16(packet[2:4])&0x8000 != 0}
	questions := int(binary.BigEndian.Uint16(packet[4:6]))
	answers := int(binary.BigEndian.Uint16(packet[6:8]))
	additionals := int(binary.BigEndian.Uint16(packet[10:12]))
	offset := headerBytes
	for i := 0; i < questions; i++ {
		name, next, err := decodeName(packet, offset)
		if err != nil {
			return testMessage{}, err
		}
		if next+4 > len(packet) {
			return testMessage{}, errMalformedPacket
		}
		message.questions = append(message.questions, question{
			name: name, qtype: binary.BigEndian.Uint16(packet[next : next+2]),
		})
		offset = next + 4
	}
	for i := 0; i < answers+additionals; i++ {
		value, next, err := parseTestRecord(packet, offset)
		if err != nil {
			return testMessage{}, err
		}
		if i < answers {
			message.answers = append(message.answers, value)
		} else {
			message.additionals = append(message.additionals, value)
		}
		offset = next
	}
	return message, nil
}

func parseTestRecord(packet []byte, offset int) (testRecord, int, error) {
	name, next, err := decodeName(packet, offset)
	if err != nil {
		return testRecord{}, 0, err
	}
	if next+10 > len(packet) {
		return testRecord{}, 0, errMalformedPacket
	}
	length := int(binary.BigEndian.Uint16(packet[next+8 : next+10]))
	start := next + 10
	if start+length > len(packet) {
		return testRecord{}, 0, errMalformedPacket
	}
	return testRecord{
		name:  name,
		rtype: binary.BigEndian.Uint16(packet[next : next+2]),
		class: binary.BigEndian.Uint16(packet[next+2 : next+4]),
		ttl:   binary.BigEndian.Uint32(packet[next+4 : next+8]),
		data:  append([]byte(nil), packet[start:start+length]...),
	}, start + length, nil
}

func findRecord(records []testRecord, name string, rtype uint16) (testRecord, bool) {
	for _, value := range records {
		if value.rtype == rtype && strings.EqualFold(value.name, name) {
			return value, true
		}
	}
	return testRecord{}, false
}

func decodeTXT(t *testing.T, data []byte) map[string]string {
	t.Helper()
	properties := make(map[string]string)
	for offset := 0; offset < len(data); {
		length := int(data[offset])
		offset++
		if length == 0 || offset+length > len(data) {
			t.Fatalf("malformed TXT property at offset %d", offset)
		}
		key, value, ok := strings.Cut(string(data[offset:offset+length]), "=")
		if !ok {
			t.Fatalf("TXT property %q has no value", data[offset:offset+length])
		}
		properties[key] = value
		offset += length
	}
	return properties
}

func decodeSRV(t *testing.T, data []byte) (uint16, string) {
	t.Helper()
	if len(data) < 7 {
		t.Fatalf("malformed SRV record: %d bytes", len(data))
	}
	target, next, err := decodeName(data, 6)
	if err != nil || next != len(data) {
		t.Fatalf("malformed SRV target: %v", err)
	}
	return binary.BigEndian.Uint16(data[4:6]), target
}

func TestQueryRoundTrip(t *testing.T) {
	ad := testAdvertisement(t)
	for _, test := range []struct {
		name  string
		qtype uint16
	}{
		{serviceType, typePTR},
		{ad.instance, typeSRV},
		{ad.instance, typeTXT},
		{ad.host, typeA},
	} {
		questions, err := parseQuery(buildTestQuery(t, test.name, test.qtype), maxQuestionsPerQuery)
		if err != nil {
			t.Fatalf("%s query: %v", test.name, err)
		}
		if len(questions) != 1 {
			t.Fatalf("%s query parsed %d questions", test.name, len(questions))
		}
		if questions[0].name != test.name || questions[0].qtype != test.qtype {
			t.Fatalf("question = %#v, want name %q type %d", questions[0], test.name, test.qtype)
		}
	}
}

func TestParseQueryRejectsResponses(t *testing.T) {
	ad := testAdvertisement(t)
	records, err := ad.records(announcementTTL)
	if err != nil {
		t.Fatal(err)
	}
	response := buildResponse(nil, records.all(), nil, maxResponseBytes)
	if _, err := parseQuery(response, maxQuestionsPerQuery); !errors.Is(err, errNotQuery) {
		t.Fatalf("parseQuery(response) error = %v, want errNotQuery", err)
	}
}

func TestParseQueryRejectsTooManyQuestions(t *testing.T) {
	packet := make([]byte, headerBytes)
	binary.BigEndian.PutUint16(packet[4:6], maxQuestionsPerQuery+1)
	_, err := parseQuery(packet, maxQuestionsPerQuery)
	if !errors.Is(err, errTooManyQuestions) {
		t.Fatalf("parseQuery error = %v, want errTooManyQuestions", err)
	}
}

func TestWrongNameAndTypeIgnored(t *testing.T) {
	ad := testAdvertisement(t)
	questions := []question{
		{name: "_other._tcp.local.", qtype: typePTR},
		{name: ad.instance, qtype: typePTR},
		{name: ad.host, qtype: typeTXT},
		{name: serviceType, qtype: typeA},
		{name: ad.host, qtype: typeSRV},
	}
	response := ad.responseFor(questions, announcementTTL)
	if len(response.answers) != 0 || len(response.additionals) != 0 {
		t.Fatalf("unmatched questions produced %#v", response)
	}
}

func TestNameMatchingIsCaseInsensitive(t *testing.T) {
	ad := testAdvertisement(t)
	questions := []question{
		{name: strings.ToUpper(serviceType), qtype: typePTR},
		{name: strings.ToUpper(ad.instance), qtype: typeSRV},
	}
	response := ad.responseFor(questions, announcementTTL)
	if len(response.answers) != 2 {
		t.Fatalf("case-insensitive query produced %d answers", len(response.answers))
	}
}

func TestPTRAnswerIncludesAdditionalRecords(t *testing.T) {
	ad := testAdvertisement(t)
	response := ad.responseFor([]question{{name: serviceType, qtype: typePTR}}, announcementTTL)
	packet := buildResponse(response.questions, response.answers, response.additionals, maxResponseBytes)
	message, err := parseTestMessage(packet)
	if err != nil {
		t.Fatal(err)
	}
	if !message.response {
		t.Fatal("response flag is not set")
	}
	if len(message.questions) != 1 || message.questions[0].name != serviceType || message.questions[0].qtype != typePTR {
		t.Fatalf("question echo = %#v", message.questions)
	}
	ptr, ok := findRecord(message.answers, serviceType, typePTR)
	if !ok {
		t.Fatal("missing PTR answer")
	}
	if ptr.ttl != announcementTTL || ptr.class != classIN {
		t.Fatalf("PTR ttl/class = %d/%d", ptr.ttl, ptr.class)
	}
	instance, next, err := decodeName(ptr.data, 0)
	if err != nil || next != len(ptr.data) || instance != ad.instance {
		t.Fatalf("PTR target = %q, %v", instance, err)
	}
	srv, ok := findRecord(message.additionals, ad.instance, typeSRV)
	if !ok {
		t.Fatal("missing SRV additional record")
	}
	port, target := decodeSRV(t, srv.data)
	if port != ad.port || target != ad.host {
		t.Fatalf("SRV = port %d target %q", port, target)
	}
	if srv.class&cacheFlush == 0 || srv.ttl != announcementTTL {
		t.Fatalf("SRV class/ttl = %#x/%d", srv.class, srv.ttl)
	}
	txt, ok := findRecord(message.additionals, ad.instance, typeTXT)
	if !ok {
		t.Fatal("missing TXT additional record")
	}
	properties := decodeTXT(t, txt.data)
	if properties["agent_id"] != testAgentID {
		t.Fatalf("TXT agent_id = %q", properties["agent_id"])
	}
	if properties["tls"] != "false" {
		t.Fatalf("TXT tls = %q", properties["tls"])
	}
	address, ok := findRecord(message.additionals, ad.host, typeA)
	if !ok {
		t.Fatal("missing A additional record")
	}
	if string(address.data) != string(ad.address[:]) {
		t.Fatalf("A record = %v", address.data)
	}
}

func TestSRVTXTAndAAnswers(t *testing.T) {
	ad := testAdvertisement(t)
	for _, test := range []struct {
		name  string
		qtype uint16
		check func(t *testing.T, message testMessage)
	}{
		{ad.instance, typeSRV, func(t *testing.T, message testMessage) {
			srv, ok := findRecord(message.answers, ad.instance, typeSRV)
			if !ok {
				t.Fatal("missing SRV answer")
			}
			port, target := decodeSRV(t, srv.data)
			if port != ad.port || target != ad.host {
				t.Fatalf("SRV = port %d target %q", port, target)
			}
			if _, ok := findRecord(message.additionals, ad.host, typeA); !ok {
				t.Fatal("missing A additional record")
			}
		}},
		{ad.instance, typeTXT, func(t *testing.T, message testMessage) {
			txt, ok := findRecord(message.answers, ad.instance, typeTXT)
			if !ok {
				t.Fatal("missing TXT answer")
			}
			if properties := decodeTXT(t, txt.data); properties["tls"] != "false" {
				t.Fatalf("TXT tls = %q", properties["tls"])
			}
		}},
		{ad.host, typeA, func(t *testing.T, message testMessage) {
			address, ok := findRecord(message.answers, ad.host, typeA)
			if !ok {
				t.Fatal("missing A answer")
			}
			if string(address.data) != string(ad.address[:]) {
				t.Fatalf("A record = %v", address.data)
			}
		}},
	} {
		response := ad.responseFor([]question{{name: test.name, qtype: test.qtype}}, announcementTTL)
		packet := buildResponse(response.questions, response.answers, response.additionals, maxResponseBytes)
		message, err := parseTestMessage(packet)
		if err != nil {
			t.Fatalf("%s response: %v", test.name, err)
		}
		test.check(t, message)
	}
}

func TestAnnouncementAndGoodbyeTTL(t *testing.T) {
	ad := testAdvertisement(t)
	for _, ttl := range []uint32{announcementTTL, 0} {
		records, err := ad.records(ttl)
		if err != nil {
			t.Fatal(err)
		}
		packet := buildResponse(nil, records.all(), nil, maxResponseBytes)
		message, err := parseTestMessage(packet)
		if err != nil {
			t.Fatal(err)
		}
		if len(message.answers) != 4 {
			t.Fatalf("ttl %d announcement has %d answers", ttl, len(message.answers))
		}
		for _, value := range message.answers {
			if value.ttl != ttl {
				t.Fatalf("ttl %d record %q has ttl %d", ttl, value.name, value.ttl)
			}
		}
	}
}

func TestResponseSizeLimit(t *testing.T) {
	ad := testAdvertisement(t)
	records, err := ad.records(announcementTTL)
	if err != nil {
		t.Fatal(err)
	}
	questions := make([]question, 0, maxQuestionsPerQuery)
	for i := 0; i < maxQuestionsPerQuery; i++ {
		questions = append(questions, question{name: serviceType, qtype: typePTR})
	}
	for _, limit := range []int{headerBytes, 50, 120, 1000, maxResponseBytes} {
		packet := buildResponse(questions, records.all(), records.all(), limit)
		if len(packet) > limit {
			t.Fatalf("limit %d produced a %d byte response", limit, len(packet))
		}
		if _, err := parseTestMessage(packet); err != nil {
			t.Fatalf("limit %d produced an unparsable response: %v", limit, err)
		}
	}
	packet := buildResponse(nil, records.all(), nil, headerBytes+1)
	message, err := parseTestMessage(packet)
	if err != nil {
		t.Fatal(err)
	}
	if len(message.answers) != 0 || len(message.additionals) != 0 {
		t.Fatalf("limited response kept %#v", message)
	}
}

func TestDecodeNameCompression(t *testing.T) {
	ad := testAdvertisement(t)
	encoded, err := encodeName(ad.host)
	if err != nil {
		t.Fatal(err)
	}
	packet := make([]byte, headerBytes)
	packet = append(packet, encoded...)
	pointer := len(packet)
	packet = append(packet, 0xc0, byte(headerBytes))
	name, next, err := decodeName(packet, pointer)
	if err != nil {
		t.Fatal(err)
	}
	if name != ad.host || next != pointer+2 {
		t.Fatalf("decoded name = %q, next = %d", name, next)
	}
	self := make([]byte, headerBytes)
	self = append(self, 0xc0, byte(headerBytes))
	if _, _, err := decodeName(self, headerBytes); err == nil {
		t.Fatal("decodeName accepted a cyclic pointer")
	}
	forward := make([]byte, headerBytes)
	forward = append(forward, 0xc0, 0x10)
	if _, _, err := decodeName(forward, headerBytes); err == nil {
		t.Fatal("decodeName accepted a forward pointer")
	}
}

func TestAgentAdvertisementValidation(t *testing.T) {
	for _, invalid := range []string{
		"",
		"0123456789abcdef0123456789abcde",
		"0123456789abcdef0123456789abcdef0",
		"0123456789ABCDEF0123456789ABCDEF",
		"0123456789abcdef0123456789abcdeg",
		"0123456789abcdef0123456789abcde\n",
	} {
		if _, err := newAdvertisement(invalid, 8787); err == nil {
			t.Errorf("agent ID %q accepted", invalid)
		}
	}
	for _, port := range []int{0, -1, 65536} {
		if _, err := newAdvertisement(testAgentID, port); err == nil {
			t.Errorf("port %d accepted", port)
		}
	}
	ad, err := newAdvertisement(testAgentID, 8787)
	if err != nil {
		t.Fatal(err)
	}
	if ad.instance != "openwrt-presence-agent-01234567._openwrt-presence-agent._tcp.local." {
		t.Fatalf("instance = %q", ad.instance)
	}
	if ad.host != "openwrt-presence-01234567.local." {
		t.Fatalf("host = %q", ad.host)
	}
}

func TestNewRefusesLoopbackAdvertisement(t *testing.T) {
	_, err := New(Config{
		AgentID: testAgentID, Port: 8787, ListenAddress: "127.0.0.1", Interface: "lo",
	}, nil)
	if !errors.Is(err, ErrLoopbackOnly) {
		t.Fatalf("New error = %v, want ErrLoopbackOnly", err)
	}
	if _, err := New(Config{
		AgentID: "not-an-agent-id", Port: 8787, ListenAddress: "10.0.0.1",
	}, nil); err == nil {
		t.Fatal("New accepted an invalid agent ID")
	}
}

func TestAdvertisedAddress(t *testing.T) {
	address, err := advertisedAddress("10.1.2.3", "ignored")
	if err != nil || address.String() != "10.1.2.3" {
		t.Fatalf("concrete address = %v, %v", address, err)
	}
	for _, loopback := range []string{"127.0.0.1", "127.8.9.10", "::1"} {
		if _, err := advertisedAddress(loopback, "lo"); !errors.Is(err, ErrLoopbackOnly) {
			t.Errorf("advertisedAddress(%q) error = %v", loopback, err)
		}
	}
	if _, err := advertisedAddress("not-an-address", "lo"); err == nil {
		t.Fatal("advertisedAddress accepted an invalid address")
	}
	// An IPv6-only listener has no IPv4 address the published A record could
	// point at, so it must be refused rather than advertised with the
	// interface address the API is not listening on.
	if _, err := advertisedAddress("2001:db8::1", "lo"); !errors.Is(err, ErrNoIPv4) {
		t.Errorf("IPv6 listener error = %v, want ErrNoIPv4", err)
	}
	name, want := interfaceWithIPv4(t)
	fallback, err := advertisedAddress("0.0.0.0", name)
	if err != nil {
		t.Fatal(err)
	}
	if !fallback.Equal(want) {
		t.Fatalf("wildcard fallback = %v, want %v", fallback, want)
	}
}

func interfaceWithIPv4(t *testing.T) (string, []byte) {
	t.Helper()
	interfaces, err := net.Interfaces()
	if err != nil {
		t.Fatal(err)
	}
	for i := range interfaces {
		if address, ok := firstIPv4(&interfaces[i], false); ok {
			return interfaces[i].Name, address
		}
	}
	t.Skip("no non-loopback IPv4 interface is available")
	return "", nil
}
