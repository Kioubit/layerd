package PeerManager

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"google.golang.org/protobuf/proto"
	"io"
	"layerd/PeerManager/pb"
	"math"
	"net"
	"net/netip"
	"sync"
	"time"
)

var (
	prefixServerStateMu    sync.Mutex
	prefixServerState             = make(map[uint8]map[netip.Prefix]*prefixDetails)
	prefixServerGeneration uint32 = 1
)

type prefixDetails struct {
	nodeID  uint64
	time    int64
	nextHop []byte
}

func prefixServer() {
	go cleanupPrefixServer()
	lAddr := &net.TCPAddr{
		IP:   nil,
		Port: 3644,
		Zone: "",
	}
	listener, err := net.ListenTCP("tcp6", lAddr)
	if err != nil {
		panic(err)
	}
	for {
		conn, err := listener.AcceptTCP()
		if err != nil {
			fmt.Println("[PrefixServer] Error accepting: ", err.Error())
			continue
		}
		go handlePrefixServerConn(conn)
	}

}

func cleanupPrefixServer() {
	for {
		time.Sleep(5 * time.Minute)
		now := time.Now().Unix()
		result := make(map[uint8]map[netip.Prefix]*prefixDetails)
		prefixServerStateMu.Lock()
		done := false
		for l3id, v := range prefixServerState {
			result[l3id] = make(map[netip.Prefix]*prefixDetails)
			for p, v := range v {
				if now-v.time < 300 {
					done = true
					result[l3id][p] = v
				}
			}
		}
		if done {
			prefixServerState = result
			incrementPrefixServerGeneration()
		}
		prefixServerStateMu.Unlock()
	}
}

func handlePrefixServerConn(conn *net.TCPConn) {
	err := conn.SetDeadline(time.Now().Add(5 * time.Second))
	if err != nil {
		_ = conn.Close()
		return
	}
	lengthB := make([]byte, 4, 4)
	_, err = io.ReadFull(conn, lengthB)
	if err != nil {
		return
	}
	length := binary.BigEndian.Uint32(lengthB)

	data := make([]byte, length)
	reader := io.LimitReader(conn, int64(length))
	_, err = io.ReadFull(reader, data)
	if err != nil {
		return
	}

	msg := &pb.CommandMessage{}
	err = proto.Unmarshal(data, msg)
	if err != nil {
		fmt.Println("[PrefixServer] PB unmarshal error", conn.RemoteAddr().String())
		return
	}
	id := msg.NodeID
	if msg.Prefixes != nil {
		fmt.Printf("[PrefixServer] Announcement from node: %s with IP: %s\n", NodeIDToString(id), conn.RemoteAddr().String())
		for _, v := range msg.Prefixes {
			addr := protoPrefixToAddr(v)
			prefix := netip.PrefixFrom(addr, int(v.PrefixLength))

			prefixServerStateMu.Lock()
			if prefixServerState[uint8(v.L3ID)] == nil {
				fmt.Println("[PrefixServer] Register L3ID:", v.L3ID)
				prefixServerState[uint8(v.L3ID)] = make(map[netip.Prefix]*prefixDetails)
			}

			if prefixServerState[uint8(v.L3ID)][prefix] != nil {
				info := prefixServerState[uint8(v.L3ID)][prefix]
				if !bytes.Equal(info.nextHop, v.NextHop) || info.nodeID != id {
					incrementPrefixServerGeneration()
				}
			} else {
				incrementPrefixServerGeneration()
			}

			prefixServerState[uint8(v.L3ID)][prefix] = &prefixDetails{
				nodeID:  id,
				time:    time.Now().Unix(),
				nextHop: v.NextHop,
			}
			prefixServerStateMu.Unlock()
		}
	} else {
		// RENEW MODE
		prefixServerStateMu.Lock()
		fmt.Printf("[PrefixServer] Keepalive from node: %s with IP: %s\n", NodeIDToString(id), conn.RemoteAddr().String())
		now := time.Now().Unix()
		for _, m := range prefixServerState {
			for _, p := range m {
				if p.nodeID == id {
					p.time = now
				}
			}
		}
		prefixServerStateMu.Unlock()
	}

	if msg.WithdrawnPrefixes != nil {
		fmt.Printf("[PrefixServer] Withdraw from node: %s with IP: %s\n", NodeIDToString(id), conn.RemoteAddr().String())
		prefixServerStateMu.Lock()
		for _, v := range msg.WithdrawnPrefixes {
			addr := protoPrefixToAddr(v)
			prefix := netip.PrefixFrom(addr, int(v.PrefixLength))
			fmt.Println("[PrefixServer] Withdraw", prefix.String())
			delete(prefixServerState[uint8(v.L3ID)], prefix)
		}
		incrementPrefixServerGeneration()
		prefixServerStateMu.Unlock()
	}

	prefixServerStateMu.Lock()
	final := marshalPrefixes(prefixServerState, prefixServerGeneration, &msg.Generation, nil)
	prefixServerStateMu.Unlock()
	_, _ = conn.Write(final)
	_ = conn.Close()
}

func incrementPrefixServerGeneration() {
	// Needs prefixServerStateMu Lock
	prefixServerGeneration++
	if prefixServerGeneration == math.MaxUint32 {
		prefixServerGeneration = 1
	}
	fmt.Println("[PrefixServer] New generation:", prefixServerGeneration)
}

func protoPrefixToAddr(p *pb.Prefix) netip.Addr {
	var addr netip.Addr
	switch p.PrefixIP.(type) {
	case *pb.Prefix_V4:
		a := p.PrefixIP.(*pb.Prefix_V4).V4
		buf := make([]byte, 4, 4)
		binary.BigEndian.PutUint32(buf, a)
		addr = netip.AddrFrom4([4]byte(buf))
	case *pb.Prefix_V6:
		a := p.PrefixIP.(*pb.Prefix_V6).V6
		addr = netip.AddrFrom16([16]byte(a))
	}
	return addr
}

func marshalPrefixes(state map[uint8]map[netip.Prefix]*prefixDetails, generation uint32, clientGeneration *uint32, toWithdraw *map[uint8]map[netip.Prefix]*prefixDetails) []byte {
	result := &pb.CommandMessage{
		NodeID:     MyNodeID,
		NodeIPv6:   IPv6ToByte(MyIPv6),
		Generation: generation,
		Prefixes:   make([]*pb.Prefix, 0),
	}

	skip := false
	if clientGeneration != nil {
		if *clientGeneration == generation {
			skip = true
		}
	}

	if !skip {
		for l3id, v := range state {
			for p, v := range v {
				prefix := &pb.Prefix{
					PrefixIP:     nil,
					PrefixLength: uint32(p.Bits()),
					L3ID:         uint32(l3id),
					NextHop:      v.nextHop,
				}

				if p.Addr().Is4() {
					temp := p.Addr().As4()
					prefix.PrefixIP = &pb.Prefix_V4{V4: binary.BigEndian.Uint32(temp[:])}
				} else {
					temp := p.Addr().As16()
					prefix.PrefixIP = &pb.Prefix_V6{V6: temp[:]}
				}

				result.Prefixes = append(result.Prefixes, prefix)
			}
		}
	}

	if toWithdraw != nil {
		for l3id, v := range *toWithdraw {
			for p, v := range v {
				prefix := &pb.Prefix{
					PrefixIP:     nil,
					PrefixLength: uint32(p.Bits()),
					L3ID:         uint32(l3id),
					NextHop:      v.nextHop,
				}

				if p.Addr().Is4() {
					temp := p.Addr().As4()
					prefix.PrefixIP = &pb.Prefix_V4{V4: binary.BigEndian.Uint32(temp[:])}
				} else {
					temp := p.Addr().As16()
					prefix.PrefixIP = &pb.Prefix_V6{V6: temp[:]}
				}

				result.WithdrawnPrefixes = append(result.WithdrawnPrefixes, prefix)
			}
		}
	}

	data, err := proto.Marshal(result)
	if err != nil {
		panic(err)
	}
	final := make([]byte, 4, len(data)+4)
	binary.BigEndian.PutUint32(final, uint32(len(data)))
	final = append(final, data...)
	return final
}
