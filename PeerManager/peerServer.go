package PeerManager

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"google.golang.org/protobuf/proto"
	"io"
	"layerd/Interfaces"
	"layerd/PeerManager/pb"
	"net"
	"net/netip"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

var MyNodeID uint64
var MyIPv6 netip.Addr
var (
	nodeState   *pb.Announcement
	nodeStateMu sync.RWMutex
)

var needResend atomic.Bool

var lastGeneration uint32 = 0
var (
	myAnnouncements   = make(map[uint8]map[netip.Prefix]*prefixDetails)
	myWithdrawList    = make(map[uint8]map[netip.Prefix]*prefixDetails)
	myAnnouncementsMu sync.Mutex
)

type deniedPrimary struct {
	IPv6 []byte
	time int64
}

var (
	denyListMu sync.Mutex
	denyList   = make([]deniedPrimary, 0)
)

func Startup() {
	needResend.Store(true)
	MyNodeID = generateNodeID()
	nodeState = &pb.Announcement{
		NodeID:          MyNodeID,
		SelectedNetwork: MyNodeID,
		NetworkIPv6:     IPv6ToByte(MyIPv6),
	}
	fmt.Println("My node ID:", NodeIDToString(nodeState.NodeID))
	go prefixServer()
	go peerAnnouncementServer()
	go prefixAnnouncementClient()
	for {
		broadcastAnnouncement()
		time.Sleep(10 * time.Second)
	}

}

func cleanupDenyList() {
	// Lock mutex before
	newDenyList := make([]deniedPrimary, 0)
	now := time.Now().Unix()
	for i := 0; i < len(denyList); i++ {
		if now-denyList[i].time <= 120 {
			newDenyList = append(newDenyList, denyList[i])
		}
	}
	denyList = newDenyList
}

func AddAnnouncement(l3id uint8, prefix netip.Prefix) {
	myAnnouncementsMu.Lock()
	defer myAnnouncementsMu.Unlock()
	if myAnnouncements[l3id] == nil {
		myAnnouncements[l3id] = make(map[netip.Prefix]*prefixDetails)
	}
	myAnnouncements[l3id][prefix] = &prefixDetails{
		nextHop: IPv6ToByte(MyIPv6),
	}
	needResend.Store(true)
}

func WithdrawAnnouncement(l3id uint8, prefix netip.Prefix) {
	myAnnouncementsMu.Lock()
	defer myAnnouncementsMu.Unlock()
	if myAnnouncements[l3id] == nil {
		return
	}
	delete(myAnnouncements[l3id], prefix)
	if myWithdrawList[l3id] == nil {
		myWithdrawList[l3id] = make(map[netip.Prefix]*prefixDetails)
	}
	myWithdrawList[l3id][prefix] = &prefixDetails{}
	needResend.Store(true)
}

func prefixAnnouncementClient() {
	count := 0
	for {
		time.Sleep(30 * time.Second)
		count++
		if count > 60 {
			count = 0
			needResend.Store(true)
		}
		nodeStateMu.RLock()
		primaryIPBytes := nodeState.NetworkIPv6
		nodeStateMu.RUnlock()
		primaryIP := netip.AddrFrom16([16]byte(primaryIPBytes))
		fmt.Println("Primary is:", primaryIP.String())
		dest := &net.TCPAddr{
			IP: net.IP{primaryIPBytes[0], primaryIPBytes[1], primaryIPBytes[2], primaryIPBytes[3], primaryIPBytes[4], primaryIPBytes[5],
				primaryIPBytes[6], primaryIPBytes[7], primaryIPBytes[8], primaryIPBytes[9], primaryIPBytes[10], primaryIPBytes[11], primaryIPBytes[12],
				primaryIPBytes[13], primaryIPBytes[14], primaryIPBytes[15]},
			Port: 3644,
		}

		conn, err := net.DialTCP("tcp6", nil, dest)
		if err != nil {
			fmt.Println("Error contacting primary", primaryIP.String(), err)
			fmt.Println("Deny listing primary")
			nodeStateMu.Lock()
			denyListMu.Lock()
			cleanupDenyList()
			denyList = append(denyList, deniedPrimary{
				IPv6: primaryIPBytes,
				time: time.Now().Unix(),
			})
			nodeState.SelectedNetwork = MyNodeID
			nodeState.NetworkIPv6 = IPv6ToByte(MyIPv6)
			lastGeneration = 0
			denyListMu.Unlock()
			nodeStateMu.Unlock()
			continue
		}

		err = conn.SetDeadline(time.Now().Add(5 * time.Second))
		if err != nil {
			_ = conn.Close()
			continue
		}
		myAnnouncementsMu.Lock()
		// If clientGeneration value equals the generation value, then sending prefixes is disabled
		clientGenerationOverride := &lastGeneration
		if needResend.Swap(false) {
			clientGenerationOverride = nil
		}

		var data []byte
		if len(myWithdrawList) > 0 {
			data = marshalPrefixes(myAnnouncements, lastGeneration, clientGenerationOverride, &myWithdrawList)
			myWithdrawList = make(map[uint8]map[netip.Prefix]*prefixDetails)
		} else {
			data = marshalPrefixes(myAnnouncements, lastGeneration, clientGenerationOverride, nil)
		}
		_, err = conn.Write(data)
		if err != nil {
			fmt.Println(err.Error())
			myAnnouncementsMu.Unlock()
			_ = conn.Close()
			continue
		}
		myAnnouncementsMu.Unlock()
		lengthB := make([]byte, 4, 4)
		_, err = io.ReadFull(conn, lengthB)
		if err != nil {
			_ = conn.Close()
			continue
		}
		length := binary.BigEndian.Uint32(lengthB)

		dataRX := make([]byte, length)
		reader := io.LimitReader(conn, int64(length))
		_, err = io.ReadFull(reader, dataRX)
		if err != nil {
			_ = conn.Close()
			continue
		}

		msg := &pb.CommandMessage{}
		err = proto.Unmarshal(dataRX, msg)
		if err != nil {
			fmt.Println("PB unmarshal error", conn.RemoteAddr().String())
			_ = conn.Close()
			continue
		}

		if msg.Generation == lastGeneration {
			fmt.Println("Already synchronized with primary - Generation: ", lastGeneration)
			_ = conn.Close()
			continue
		} else {
			needResend.Store(true) // Remote may have lost state
		}

		lastGeneration = msg.Generation
		id := msg.NodeID
		if msg.Prefixes != nil {
			finalPrefixList := make(map[uint8][]Interfaces.RouteDetails)
			fmt.Println("Received prefix data from primary", NodeIDToString(id))
			for _, v := range msg.Prefixes {
				var addr netip.Addr
				switch v.PrefixIP.(type) {
				case *pb.Prefix_V4:
					a := v.PrefixIP.(*pb.Prefix_V4).V4
					buf := make([]byte, 4, 4)
					binary.BigEndian.PutUint32(buf, a)
					addr = netip.AddrFrom4([4]byte(buf))
				case *pb.Prefix_V6:
					a := v.PrefixIP.(*pb.Prefix_V6).V6
					addr = netip.AddrFrom16([16]byte(a))
				}
				prefix := netip.PrefixFrom(addr, int(v.PrefixLength))
				n := &Interfaces.RouteDetails{
					Prefix:  prefix,
					NextHop: ByteToIPv6(v.NextHop),
				}
				if n.NextHop != MyIPv6 {
					// Store for lookup
					if finalPrefixList[uint8(v.L3ID)] == nil {
						finalPrefixList[uint8(v.L3ID)] = make([]Interfaces.RouteDetails, 0)
					}
					finalPrefixList[uint8(v.L3ID)] = append(finalPrefixList[uint8(v.L3ID)], *n)
				}
			}
			for i, details := range finalPrefixList {
				sort.Slice(details, func(i, j int) bool {
					return details[i].Prefix.Bits() > details[j].Prefix.Bits()
				})
				for _, p := range details {
					fmt.Println("Updated route", p.Prefix.String(), "nextHop", p.NextHop.String(), "l3id", i)
				}
				Interfaces.LookupArray[i].Store(&details)
			}
		}

		_ = conn.Close()
	}
}

func peerAnnouncementServer() {
	addr := net.UDPAddr{
		IP:   nil,
		Port: 3644,
		Zone: "",
	}
	ser, err := net.ListenUDP("udp6", &addr)
	if err != nil {
		panic("failed setting up announcement listener: " + err.Error())
	}
	packetBuf := make([]byte, 2048)
	for {
		readLen, _, err := ser.ReadFromUDP(packetBuf)
		if err != nil {
			fmt.Printf("Some error  %v", err)
			continue
		}

		msg := &pb.Announcement{}
		err = proto.Unmarshal(packetBuf[:readLen], msg)
		if err != nil {
			fmt.Printf("Protobuf unmarshal error %s\n", err)
			continue
		}
		if msg.NodeID == MyNodeID {
			continue
		}
		nodeStateMu.Lock()
		if msg.SelectedNetwork < nodeState.SelectedNetwork {
			denyListMu.Lock()
			cancel := false
			cleanupDenyList()
			for i := 0; i < len(denyList); i++ {
				if bytes.Equal(msg.NetworkIPv6, denyList[i].IPv6) {
					fmt.Println("Blocking denied Primary at", ByteToIPv6(denyList[i].IPv6))
					cancel = true
				}
			}
			denyListMu.Unlock()
			if !cancel {
				nodeState.SelectedNetwork = msg.SelectedNetwork
				nodeState.NetworkIPv6 = msg.NetworkIPv6
				lastGeneration = 0
				needResend.Store(true)
				fmt.Printf("New primary candidate: %s -> %s\n", NodeIDToString(msg.SelectedNetwork), ByteToIPv6(msg.NetworkIPv6).String())
			}
		}
		nodeStateMu.Unlock()
	}
}

func broadcastAnnouncement() {
	destinationIP := &net.UDPAddr{
		IP:   net.ParseIP("ff02::1"),
		Port: 3644,
	}
	nodeStateMu.RLock()
	message, err := proto.Marshal(nodeState)
	nodeStateMu.RUnlock()
	if err != nil {
		panic(err)
	}
	for _, v := range getAllInterfaces() {
		destinationIP.Zone = v
		conn, err := net.DialUDP("udp6", nil, destinationIP)
		if err != nil {
			continue
		}
		_, err = conn.Write(message)
		if err != nil {
			continue
		}
	}
}
